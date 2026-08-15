package smtpcheck_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/features/smtpcheck"
	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/golden"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// fakeSession answers with fixed capabilities and records the credential it was given.
type fakeSession struct {
	caps   mksmtp.Capabilities
	closed bool
}

func (f *fakeSession) Send(mksmtp.Message) error         { return nil }
func (f *fakeSession) Capabilities() mksmtp.Capabilities { return f.caps }
func (f *fakeSession) Close()                            { f.closed = true }

// capabilities is a well-configured server, for the cases that are about the report.
func capabilities() mksmtp.Capabilities {
	return mksmtp.Capabilities{
		StartTLS: true, Pipelining: true, EightBitMIME: true,
		Auth: []string{"PLAIN"}, MaxSize: 20971520,
		TLSVersion: "TLS 1.3", CipherSuite: "TLS_AES_256_GCM_SHA384",
		CertificateSubject: "smtp.mailkube.com", CertificateIssuer: "Example CA",
		// Eighty days after the fixed test clock, so the remaining-life figure is stable.
		CertificateExpiry: clock.Testing().Now().Add(80 * 24 * time.Hour),
	}
}

// result is one run, captured whole.
type result struct {
	out    string
	errOut string
	code   errs.Code
	config mksmtp.Config
}

// run executes `smtp test` against a fake session.
func run(t *testing.T, session *fakeSession, opts testsupport.TestOptions, args ...string) result {
	t.Helper()

	if opts.Globals == nil {
		opts.Globals = &settings.Globals{Output: "text"}
	}
	deps, out, errOut := testsupport.TestDeps(t, opts)

	var used mksmtp.Config
	f := smtpcheck.New()
	f.Connect = func(_ context.Context, config mksmtp.Config) (ports.SMTPSubmitter, error) {
		used = config
		return session, nil
	}

	cmd := f.Command(deps)
	cmd.SetArgs(testsupport.Args(args))
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	code := errs.CodeOK
	if err := cmd.Execute(); err != nil {
		detail := errs.Describe(err)
		code = detail.Code
		errOut.WriteString(strings.Join(errs.Render(detail, deps.Caps.Glyphs.Cross), "\n"))
	}
	return result{out: out.String(), errOut: errOut.String(), code: code, config: used}
}

// configured is the environment of someone who has set up submission.
func configured() testsupport.TestOptions {
	return testsupport.TestOptions{
		Env: map[string]string{
			settings.EnvSMTPPassword: "secret",
		},
		Globals: &settings.Globals{Output: "text"},
	}
}

func TestTheDefaultProbePutsNoCredentialOnTheWire(t *testing.T) {
	t.Parallel()

	session := &fakeSession{caps: capabilities()}
	got := run(t, session, configured(), "test", "--host", "smtp.mailkube.com")

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	// The whole reason the default is safe to run repeatedly: there is nothing in it that a
	// server could reject, so it cannot contribute to a sign-in rate limit.
	if got.config.Username != "" || got.config.Password != "" {
		t.Errorf("a credential was configured for a probe that did not ask for one: %+v", got.config)
	}
	if !strings.Contains(got.out, "No credential was sent") {
		t.Errorf("the report does not say a credential was withheld:\n%s", got.out)
	}
	if !session.closed {
		t.Error("the session was left open")
	}
}

func TestTheReportShowsParsedTokensAndTheVerifiedCertificate(t *testing.T) {
	t.Parallel()

	got := run(t, &fakeSession{caps: capabilities()}, configured(), "test", "--host", "smtp.mailkube.com")

	// Registered protocol keywords this program parsed, not the server's free text. The
	// difference matters: one is a value the CLI derived, the other is whatever the server
	// chose to write.
	for _, want := range []string{"STARTTLS", "PIPELINING", "8BITMIME", "SIZE=20971520", "AUTH=PLAIN"} {
		if !strings.Contains(got.out, want) {
			t.Errorf("the report is missing %q:\n%s", want, got.out)
		}
	}
	// Verified identity, which is the whole answer to "am I really talking to Mailkube".
	// An intercepting gateway would have failed the handshake before this line exists.
	if !strings.Contains(got.out, "valid for smtp.mailkube.com") {
		t.Errorf("the certificate is not described as verified:\n%s", got.out)
	}
	if !strings.Contains(got.out, "80 days") {
		t.Errorf("the certificate's remaining life is missing:\n%s", got.out)
	}
}

func TestAuthIsOptInAndReported(t *testing.T) {
	t.Parallel()

	opts := configured()
	opts.Env[settings.EnvSMTPPassword] = "secret"

	got := run(t, &fakeSession{caps: capabilities()}, opts,
		"test", "--auth", "--host", "smtp.mailkube.com", "--user", "app01@acme.com")

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if got.config.Username != "app01@acme.com" || got.config.Password != "secret" {
		t.Errorf("the credential did not reach the session: %+v", got.config)
	}
	if !strings.Contains(got.out, "accepted as app01@acme.com") {
		t.Errorf("the report does not confirm the credential:\n%s", got.out)
	}
}

func TestABareUsernameNeverOpensASocket(t *testing.T) {
	t.Parallel()

	opened := false
	deps, out, errOut := testsupport.TestDeps(t, configured())

	f := smtpcheck.New()
	f.Connect = func(context.Context, mksmtp.Config) (ports.SMTPSubmitter, error) {
		opened = true
		return &fakeSession{}, nil
	}

	cmd := f.Command(deps)
	cmd.SetArgs([]string{"test", "--auth", "--host", "smtp.mailkube.com", "--user", "myapp01"})
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("a username with no domain was accepted")
	}
	// The refusal is local because the consequence is not: repeated malformed sign-ins are
	// what get an address blocked, and the block outlives the typo by a long way.
	if opened {
		t.Error("a socket was opened for a username that cannot be valid")
	}
	if errs.CodeFor(err) != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", errs.CodeFor(err), errs.CodeValidation)
	}
}

func TestAuthWithoutAUsernameSaysHowToSupplyOne(t *testing.T) {
	t.Parallel()

	got := run(t, &fakeSession{}, configured(), "test", "--auth", "--host", "smtp.mailkube.com")
	if got.code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeConfig)
	}
	if !strings.Contains(got.errOut, "auth login --smtp") {
		t.Errorf("the refusal does not say how to configure one:\n%s", got.errOut)
	}
}

func TestNoHostIsAConfigurationErrorRatherThanADialToNowhere(t *testing.T) {
	t.Parallel()

	got := run(t, &fakeSession{}, testsupport.TestOptions{
		Globals: &settings.Globals{Output: "text"},
	}, "test")

	if got.code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeConfig)
	}
}

func TestTheSubmissionDefaultsMakeAHostEnough(t *testing.T) {
	t.Parallel()

	// 587 with STARTTLS is what a submission service is expected to offer, so configuring a
	// host alone has to be enough or the common case is all ceremony.
	got := run(t, &fakeSession{caps: capabilities()}, configured(), "test", "--host", "smtp.mailkube.com")

	if got.config.Port != 587 {
		t.Errorf("port = %d, want the default 587", got.config.Port)
	}
	if got.config.TLS != mksmtp.STARTTLS {
		t.Errorf("tls = %q, want the default starttls", got.config.TLS)
	}
}

func TestScreens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		auth bool
	}{
		{name: "smtp_test", args: []string{"test", "--host", "smtp.mailkube.com"}},
		{
			name: "smtp_test_auth",
			args: []string{"test", "--auth", "--host", "smtp.mailkube.com", "--user", "app01@acme.com"},
			auth: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := run(t, &fakeSession{caps: capabilities()}, configured(), tc.args...)
			if got.code != errs.CodeOK {
				t.Fatalf("exit code = %d: %s", got.code, got.errOut)
			}
			golden.Assert(t, tc.name+".out", []byte(got.out))
			golden.Assert(t, tc.name+".err", []byte(got.errOut))
		})
	}
}

// TestTheASCIIGlyphSetRendersToo guards the fallback terminal, which no other screen test covers
// for this feature.
func TestTheASCIIGlyphSetRendersToo(t *testing.T) {
	t.Parallel()

	opts := configured()
	opts.Caps = &output.Caps{Unicode: false, Width: output.DefaultWidth, Glyphs: output.ASCIIGlyphs()}

	got := run(t, &fakeSession{caps: capabilities()}, opts, "test", "--host", "smtp.mailkube.com")
	if !strings.Contains(got.out, "[ok]") {
		t.Errorf("the ASCII badge set was not used:\n%s", got.out)
	}
}
