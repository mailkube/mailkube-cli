package auth_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/meta/auth"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// fakeSender answers the credential probe with a fixed result.
type fakeSender struct {
	err  error
	sent int
}

func (f *fakeSender) Send(context.Context, mailkube.SendEmailParams) (*mailkube.Email, error) {
	f.sent++
	return nil, f.err
}

// featureWith returns the auth feature wired to a fixed probe answer.
func featureWith(sender ports.EmailSender) *auth.Feature {
	f := auth.New()
	f.Sender = func(*feature.Deps, settings.Resolved) (ports.EmailSender, error) { return sender, nil }
	return f
}

// TestTheProbeReadsTheRejectionAsProofOfAuthentication is the point of the whole mechanism: the
// only response that proves a key is valid is the one rejecting the from domain, because that
// check runs after authentication and before anything is charged.
func TestTheProbeReadsTheRejectionAsProofOfAuthentication(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{err: &mailkube.APIError{
		ErrorName:  mailkube.ErrorNameFromDomainNotAllowed,
		Message:    "From address must match the key's domain acme.com",
		StatusCode: 422,
		RequestID:  "8f2c1ad4e93b4c7fa10d5e2b9c46f183",
	}}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	view, err := featureWith(sender).LoginAPI(context.Background(), deps, "mk_key", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if !view.Verification.Verified {
		t.Error("a from_domain_not_allowed rejection was not read as a successful authentication")
	}
	// The server's own words are carried through untouched. They name the domain the key is
	// bound to, and parsing that out of prose would break on the first wording change.
	if !strings.Contains(view.Verification.Message, "acme.com") {
		t.Errorf("the server's message was not carried through: %q", view.Verification.Message)
	}
	if !view.Stored {
		t.Error("a verified key was not stored")
	}
}

func TestARejectedKeyIsNeverStored(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{err: &mailkube.APIError{
		ErrorName: mailkube.ErrorNameInvalidAPIKey, Message: "The API key is not valid.", StatusCode: 403,
	}}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	_, err := featureWith(sender).LoginAPI(context.Background(), deps, "mk_bad", false)
	if err == nil {
		t.Fatal("a rejected key was accepted")
	}
	if code := errs.CodeFor(err); code != errs.CodeAuth {
		t.Errorf("exit code = %d, want %d", code, errs.CodeAuth)
	}

	// The file must not have been touched: storing first and checking afterwards would leave a
	// key the server refused sitting in the config.
	cfg, err := deps.Store.Load()
	if err != nil {
		t.Fatalf("loading the config: %v", err)
	}
	if cfg.Profiles["default"].APIKey != "" {
		t.Error("a rejected key was written to the config file")
	}
}

func TestAnUnrecognisedAnswerIsInconclusiveRatherThanFatal(t *testing.T) {
	t.Parallel()

	// The platform answering in a way this release has not seen is not evidence that the key is
	// bad, and refusing to store a working credential over it would be the wrong trade.
	sender := &fakeSender{err: &mailkube.APIError{ErrorName: "something_new", StatusCode: 418}}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	view, err := featureWith(sender).LoginAPI(context.Background(), deps, "mk_key", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if view.Verification.Verified {
		t.Error("an unrecognised answer was reported as a successful verification")
	}
	if !view.Stored {
		t.Error("an inconclusive check prevented a credential from being stored")
	}
}

func TestNoVerifyContactsNothing(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	if _, err := featureWith(sender).LoginAPI(context.Background(), deps, "mk_key", true); err != nil {
		t.Fatalf("login: %v", err)
	}
	if sender.sent != 0 {
		t.Errorf("--no-verify made %d request(s); provisioning offline is the whole point of it", sender.sent)
	}
}

func TestABareSMTPUsernameNeverReachesTheWire(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{settings.EnvSMTPPassword: "secret"},
	})

	_, err := auth.New().LoginSMTP(deps, "myapp01")
	if err == nil {
		t.Fatal("a username with no domain was accepted")
	}
	if code := errs.CodeFor(err); code != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", code, errs.CodeValidation)
	}
}

func TestTheTwoCredentialsAreStoredIndependently(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{settings.EnvSMTPPassword: "secret"},
	})

	credentials := featureWith(&fakeSender{})
	if _, err := credentials.LoginAPI(context.Background(), deps, "mk_key", true); err != nil {
		t.Fatalf("api login: %v", err)
	}
	if _, err := credentials.LoginSMTP(deps, "app01@acme.com"); err != nil {
		t.Fatalf("smtp login: %v", err)
	}

	cfg, err := deps.Store.Load()
	if err != nil {
		t.Fatalf("loading the config: %v", err)
	}
	profile := cfg.Profiles["default"]
	if profile.APIKey == "" {
		t.Error("storing the SMTP credential dropped the API key")
	}
	if profile.SMTP == nil || profile.SMTP.Username != "app01@acme.com" {
		t.Error("the SMTP credential was not stored")
	}
	if profile.SMTP != nil && profile.SMTP.Password != "secret" {
		t.Error("the password was not taken from the environment")
	}
}

func TestTheStoredKeyIsNeverRenderedInFull(t *testing.T) {
	t.Parallel()

	const key = "mk_j3k1a2b3c4d5f8a2"
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	view, err := featureWith(&fakeSender{}).LoginAPI(context.Background(), deps, key, true)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	rendered := strings.Join(view.RenderText(output.Caps{Glyphs: output.ASCIIGlyphs()}), "\n")
	if strings.Contains(rendered, key) {
		t.Errorf("the whole key was printed:\n%s", rendered)
	}
	// Both ends are shown, because telling two keys apart is the entire reason it is printed.
	if !strings.Contains(rendered, "mk_j3k") || !strings.Contains(rendered, "f8a2") {
		t.Errorf("the masked key is not recognisable:\n%s", rendered)
	}
}

// interactive is the terminal model for a run that is allowed to ask questions.
func interactive() *output.Caps {
	return &output.Caps{
		TTY: true, Unicode: true, Interactive: true,
		Width: output.DefaultWidth, Glyphs: output.UnicodeGlyphs(),
	}
}

func TestTheGuidedSetupWalksBothCredentials(t *testing.T) {
	t.Parallel()

	// The answers, in order: the key, yes to SMTP, the username. The password comes from the
	// environment, because a password is never taken from an argument.
	deps, out, errOut := testsupport.TestDeps(t, testsupport.TestOptions{
		Caps:  interactive(),
		Stdin: "mk_j3k1a2b3c4d5f8a2\ny\napp01@acme.com\n",
		Env:   map[string]string{settings.EnvSMTPPassword: "secret"},
	})
	deps.Format = output.Text

	credentials := featureWith(&fakeSender{err: &mailkube.APIError{
		ErrorName: mailkube.ErrorNameFromDomainNotAllowed,
		Message:   "From address must match the key's domain acme.com",
	}})

	cmd := auth.NewInit(credentials).Command(deps)
	cmd.SetArgs(nil)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Each step is reported as it happens, on the progress stream. A wizard that asked the next
	// question before showing the result of the last one would have the user deciding whether
	// to add SMTP credentials without yet knowing whether their key even worked.
	progress := errOut.String()
	if !strings.Contains(progress, "Verified") {
		t.Errorf("the key's verdict was not reported as progress:\n%s", progress)
	}
	if strings.Index(progress, "Verified") > strings.Index(progress, "Add SMTP credentials") {
		t.Errorf("the SMTP question was asked before the key's verdict:\n%s", progress)
	}
	if !strings.Contains(out.String(), "You're set up") {
		t.Errorf("the payload does not close the setup:\n%s", out.String())
	}

	cfg, err := deps.Store.Load()
	if err != nil {
		t.Fatalf("loading the config: %v", err)
	}
	profile := cfg.Profiles["default"]
	if profile.APIKey == "" || profile.SMTP == nil || profile.SMTP.Username != "app01@acme.com" {
		t.Errorf("the guided setup did not store both credentials: %+v", profile)
	}
}

func TestDecliningSMTPIsRecordedRatherThanForgotten(t *testing.T) {
	t.Parallel()

	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Caps:  interactive(),
		Stdin: "mk_j3k1a2b3c4d5f8a2\nn\n",
	})
	deps.Format = output.Text

	cmd := auth.NewInit(featureWith(&fakeSender{})).Command(deps)
	cmd.SetArgs([]string{"--no-verify"})
	cmd.SetOut(out)
	cmd.SetErr(deps.IO.ErrOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Declining is different from never having been asked, and the screen says which happened.
	if !strings.Contains(out.String(), "auth login --smtp") {
		t.Errorf("declining did not name the way back:\n%s", out.String())
	}
}

func TestAnEmptyAnswerIsNotACredential(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Caps:  interactive(),
		Stdin: "\n",
	})

	_, err := auth.New().LoginAPI(context.Background(), deps, "", true)
	if err == nil {
		t.Fatal("an empty key was stored")
	}
	if code := errs.CodeFor(err); code != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", code, errs.CodeValidation)
	}
}

func TestTheKeyIsTakenFromTheEnvironmentBeforeAskingForIt(t *testing.T) {
	t.Parallel()

	// Nothing on stdin: if the environment were not consulted first, this would block or fail.
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{settings.EnvAPIKey: "mk_from_env"},
	})

	view, err := auth.New().LoginAPI(context.Background(), deps, "", true)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !view.Stored {
		t.Error("the key from the environment was not stored")
	}
}

func TestAPasswordIsAskedForRatherThanTakenFromAnArgument(t *testing.T) {
	t.Parallel()

	deps, _, errOut := testsupport.TestDeps(t, testsupport.TestOptions{
		Caps:  interactive(),
		Stdin: "typed-password\n",
	})

	if _, err := auth.New().LoginSMTP(deps, "app01@acme.com"); err != nil {
		t.Fatalf("smtp login: %v", err)
	}
	if !strings.Contains(errOut.String(), "input hidden") {
		t.Errorf("the password was not asked for on the progress stream: %q", errOut.String())
	}

	cfg, _ := deps.Store.Load()
	if cfg.Profiles["default"].SMTP.Password != "typed-password" {
		t.Error("the typed password was not stored")
	}
}

func TestAnEmptyPasswordIsRefused(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Caps:  interactive(),
		Stdin: "\n",
	})

	_, err := auth.New().LoginSMTP(deps, "app01@acme.com")
	if code := errs.CodeFor(err); code != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d (%v)", code, errs.CodeValidation, err)
	}
}

func TestLogoutRemovesOneCredentialAndKeepsTheOther(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{settings.EnvSMTPPassword: "secret"},
	})
	credentials := featureWith(&fakeSender{})

	if _, err := credentials.LoginAPI(context.Background(), deps, "mk_key", true); err != nil {
		t.Fatalf("api login: %v", err)
	}
	if _, err := credentials.LoginSMTP(deps, "app01@acme.com"); err != nil {
		t.Fatalf("smtp login: %v", err)
	}

	// Signing out of REST has no bearing on submission: two principals, removed independently.
	deps.Format = output.Text
	if err := runCommand(t, credentials, deps, "logout"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(deps.IO.Out.(*bytes.Buffer).String(), "api credential") {
		t.Error("logout did not say which credential it removed")
	}
	cfg, _ := deps.Store.Load()
	if cfg.Profiles["default"].APIKey != "" {
		t.Error("the API key survived logout")
	}
	if cfg.Profiles["default"].SMTP == nil {
		t.Error("logout removed the SMTP credential too")
	}

	if err := runCommand(t, credentials, deps, "logout", "--smtp"); err != nil {
		t.Fatalf("logout --smtp: %v", err)
	}
	cfg, _ = deps.Store.Load()
	if cfg.Profiles["default"].SMTP != nil {
		t.Error("the SMTP credential survived logout --smtp")
	}
	// The profile itself is a larger thing than the verb names, so it stays.
	if _, still := cfg.Profiles["default"]; !still {
		t.Error("logout deleted the profile")
	}
}

func TestLogoutFromAProfileThatDoesNotExistIsNotFound(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	err := runCommand(t, auth.New(), deps, "logout")
	if code := errs.CodeFor(err); code != errs.CodeNotFound {
		t.Errorf("exit code = %d, want %d (%v)", code, errs.CodeNotFound, err)
	}
}

// runCommand executes one auth subcommand through the feature's own command tree.
func runCommand(t *testing.T, f *auth.Feature, deps *feature.Deps, args ...string) error {
	t.Helper()

	cmd := f.Command(deps)
	cmd.SetArgs(args)
	cmd.SetOut(deps.IO.Out)
	cmd.SetErr(deps.IO.ErrOut)
	return cmd.Execute()
}
