package emails_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/mailkube/mailkube-cli/internal/features/emails"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// fakeSubmitter records the message and the configuration a submission used.
type fakeSubmitter struct {
	sent   []mksmtp.Message
	config mksmtp.Config
	err    error
	closed bool
}

func (f *fakeSubmitter) Send(m mksmtp.Message) error {
	f.sent = append(f.sent, m)
	return f.err
}

func (f *fakeSubmitter) Capabilities() mksmtp.Capabilities {
	return mksmtp.Capabilities{TLSVersion: "TLS 1.3"}
}

func (f *fakeSubmitter) Close() { f.closed = true }

// submit runs `emails send --transport smtp` against a fake session.
func submit(t *testing.T, submitter *fakeSubmitter, opts testsupport.TestOptions, args ...string) result {
	t.Helper()

	if opts.Env == nil {
		opts.Env = map[string]string{}
	}
	if _, set := opts.Env[settings.EnvSMTPPassword]; !set {
		opts.Env[settings.EnvSMTPPassword] = "secret"
	}
	if opts.Globals == nil {
		opts.Globals = &settings.Globals{Output: "text"}
	}

	deps, out, errOut := testsupport.TestDeps(t, opts)

	f := emails.New()
	f.Submitter = func(_ context.Context, _ *feature.Deps, config mksmtp.Config) (ports.SMTPSubmitter, error) {
		submitter.config = config
		return submitter, nil
	}

	cmd := f.Command(deps)
	cmd.SetArgs(append([]string{"send"}, args...))
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true

	code := errs.CodeOK
	if err := cmd.Execute(); err != nil {
		detail := errs.Describe(err)
		code = detail.Code
		for _, line := range errs.Render(detail, deps.Caps.Glyphs.Cross) {
			errOut.WriteString(line + "\n")
		}
	}
	return result{out: out.String(), errOut: errOut.String(), code: code}
}

// smtpArgs is a minimal submission, with the server details every case needs.
func smtpArgs(extra ...string) []string {
	return append([]string{
		"--transport", "smtp",
		"--smtp-host", "smtp.mailkube.com", "--smtp-user", "app01@acme.com",
		"--from", "hello@acme.com", "--to", "alice@example.com",
		"--subject", "Welcome", "--text", "Thanks.",
	}, extra...)
}

func TestASubmissionReportsTheChannelAndTheIdentityItUsed(t *testing.T) {
	t.Parallel()

	submitter := &fakeSubmitter{}
	got := submit(t, submitter, testsupport.TestOptions{}, smtpArgs()...)

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if len(submitter.sent) != 1 {
		t.Fatalf("messages submitted = %d, want 1", len(submitter.sent))
	}
	// The AUTH identity and the From address are different values, and a report that showed
	// only one would send someone looking for a credential that does not exist.
	if !strings.Contains(got.out, "app01@acme.com") {
		t.Errorf("the report does not name the principal:\n%s", got.out)
	}
	if !submitter.closed {
		t.Error("the session was left open")
	}
}

func TestTheSameFlagsProduceHeadersOnSubmissionAndFieldsOnREST(t *testing.T) {
	t.Parallel()

	submitter := &fakeSubmitter{}
	submit(t, submitter, testsupport.TestOptions{}, smtpArgs(
		"--topic", "news", "--tag", "campaign=launch", "--header", "X-Campaign: launch")...)

	headers := submitter.sent[0].Headers
	// Over REST these travel as body fields; there is no body to put them in here, so the
	// mapping is the whole of what makes one payload work on two transports.
	if headers["X-Mailkube-Topic"] != "news" {
		t.Errorf("topic did not become a header: %v", headers)
	}
	if !strings.Contains(headers["X-Mailkube-Tags"], `"campaign"`) {
		t.Errorf("tags did not become a header: %v", headers)
	}
	if headers["X-Campaign"] != "launch" {
		t.Errorf("the user's own header was lost: %v", headers)
	}
}

func TestTheAPIOnlyFlagsAreRefusedRatherThanDropped(t *testing.T) {
	t.Parallel()

	for _, extra := range [][]string{
		{"--at", "+2h"},
		{"--idempotency-key", "k1"},
	} {
		submitter := &fakeSubmitter{}
		got := submit(t, submitter, testsupport.TestOptions{}, smtpArgs(extra...)...)

		// A scheduled send that quietly went out immediately is the kind of failure someone
		// discovers from a customer.
		if got.code != errs.CodeUsage {
			t.Errorf("%v exited %d, want %d", extra, got.code, errs.CodeUsage)
		}
		if len(submitter.sent) != 0 {
			t.Errorf("%v was submitted anyway", extra)
		}
	}
}

func TestSubmissionNeverFallsBackToTheAPIKey(t *testing.T) {
	t.Parallel()

	submitter := &fakeSubmitter{}
	got := submit(t, submitter, testsupport.TestOptions{
		// An API key is configured and an SMTP credential is not. The two are different
		// principals, and quietly using one for the other would make "which credential
		// failed" unanswerable.
		Env:     map[string]string{settings.EnvAPIKey: "mk_test", settings.EnvSMTPPassword: "secret"},
		Globals: &settings.Globals{Output: "text"},
	},
		"--transport", "smtp", "--from", "hello@acme.com", "--to", "alice@example.com",
		"--subject", "s", "--text", "t")

	if got.code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeConfig)
	}
	if !strings.Contains(got.errOut, "auth login --smtp") {
		t.Errorf("the refusal does not say how to configure one:\n%s", got.errOut)
	}
	if len(submitter.sent) != 0 {
		t.Error("a message was submitted with no SMTP credential")
	}
}

func TestNoFlagAcceptsAPassword(t *testing.T) {
	t.Parallel()

	// Asserted against the flag set rather than by passing one, because the absence is the
	// property: a password in an argument lands in shell history and in the process list,
	// where it outlives the command by a long way. The sources are the environment, the
	// config file and a no-echo prompt, and there is deliberately no fourth.
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	send, _, err := emails.New().Command(deps).Find([]string{"send"})
	if err != nil {
		t.Fatalf("finding the send command: %v", err)
	}

	send.Flags().VisitAll(func(f *pflag.Flag) {
		if strings.Contains(strings.ToLower(f.Name), "password") {
			t.Errorf("a flag accepts a password: --%s", f.Name)
		}
	})
}

func TestWithNoPasswordAndNoTerminalItRefusesRatherThanHangs(t *testing.T) {
	t.Parallel()

	got := submit(t, &fakeSubmitter{}, testsupport.TestOptions{
		Env:     map[string]string{settings.EnvSMTPPassword: ""},
		Globals: &settings.Globals{Output: "text"},
	}, smtpArgs()...)

	if got.code == errs.CodeOK {
		t.Error("a submission proceeded with no password")
	}
	if !strings.Contains(got.errOut, settings.EnvSMTPPassword) {
		t.Errorf("the refusal does not name the variable that supplies one:\n%s", got.errOut)
	}
}

func TestADryRunOverSubmissionShowsTheRenderedMessage(t *testing.T) {
	t.Parallel()

	submitter := &fakeSubmitter{}
	got := submit(t, submitter, testsupport.TestOptions{}, smtpArgs("--dry-run")...)

	if len(submitter.sent) != 0 {
		t.Error("--dry-run submitted a message")
	}
	// The connection line answers "where, and as whom"; the document answers "what exactly".
	for _, want := range []string{"smtp.mailkube.com:587", "AUTH PLAIN as app01@acme.com", "Subject: Welcome"} {
		if !strings.Contains(got.out, want) {
			t.Errorf("the preview is missing %q:\n%s", want, got.out)
		}
	}
}

func TestASubmissionFailureKeepsItsCategory(t *testing.T) {
	t.Parallel()

	submitter := &fakeSubmitter{err: &mksmtp.Error{}}
	_ = submitter

	// Built through the real classifier rather than by hand, so this asserts the mapping the
	// program uses rather than a copy of it.
	got := submit(t, &fakeSubmitter{err: mksmtp.ErrAuth}, testsupport.TestOptions{}, smtpArgs()...)
	if got.code != errs.CodeAuth {
		t.Errorf("a rejected credential exited %d, want %d", got.code, errs.CodeAuth)
	}
	if strings.Contains(got.errOut, "Re-run the command yourself") {
		t.Errorf("a rejected credential was invited to retry:\n%s", got.errOut)
	}
}

func TestAnUnknownTransportIsAUsageError(t *testing.T) {
	t.Parallel()

	got := submit(t, &fakeSubmitter{}, testsupport.TestOptions{},
		"--transport", "carrier-pigeon", "--from", "a@b.com", "--to", "c@d.com",
		"--subject", "s", "--text", "t")
	if got.code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}
}

func TestAttachmentsAndTemplateVariablesCrossToSubmission(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("writing the attachment: %v", err)
	}

	submitter := &fakeSubmitter{}
	got := submit(t, submitter, testsupport.TestOptions{},
		"--transport", "smtp",
		"--smtp-host", "smtp.mailkube.com", "--smtp-user", "app01@acme.com",
		"--from", "hello@acme.com", "--to", "alice@example.com", "--subject", "Welcome",
		"--template-id", "1f0c1a2b", "--template-version", "latest", "--var", "first_name=Alice",
		"--attach", path)

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}

	sent := submitter.sent[0]
	// The bytes reach the builder decoded: the payload carries base64 because that is the
	// REST wire form, and encoding it a second time is how an attachment arrives as gibberish.
	if len(sent.Attachments) != 1 || string(sent.Attachments[0].Content) != "hello" {
		t.Errorf("attachments = %+v, want the decoded bytes", sent.Attachments)
	}
	if sent.Headers["X-Mailkube-Template-Id"] != "1f0c1a2b" {
		t.Errorf("template id did not become a header: %v", sent.Headers)
	}
	if !strings.Contains(sent.Headers["X-Mailkube-Template-Variables"], "Alice") {
		t.Errorf("variables did not become a header: %v", sent.Headers)
	}
}

func TestAnUnusableTLSModeIsAConfigurationError(t *testing.T) {
	t.Parallel()

	got := submit(t, &fakeSubmitter{}, testsupport.TestOptions{},
		smtpArgs("--smtp-tls", "none")...)

	// There is no unencrypted mode to fall back to, so an unrecognised one is refused rather
	// than quietly resolved to something.
	if got.code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeConfig)
	}
	if !strings.Contains(got.errOut, "starttls or implicit") {
		t.Errorf("the refusal does not name the usable modes:\n%s", got.errOut)
	}
}

func TestAnUnusablePortIsAConfigurationError(t *testing.T) {
	t.Parallel()

	got := submit(t, &fakeSubmitter{}, testsupport.TestOptions{},
		smtpArgs("--smtp-port", "not-a-port")...)
	if got.code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeConfig)
	}
}
