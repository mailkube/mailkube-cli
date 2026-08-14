package emails_test

import (
	"context"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/emails"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// fakeSender stands in for the SDK's email service, recording what it was asked to send.
//
// It records rather than only answering, because half of what these tests check is what reached
// the wire: a flag that never made it into the parameters is invisible in the output.
type fakeSender struct {
	// calls counts submissions, which is how "nothing was sent" is asserted rather than hoped.
	calls int
	// last is the parameters of the most recent submission.
	last mailkube.SendEmailParams
	// email is what to answer with. Nil answers with a plain accepted send.
	email *mailkube.Email
	// err is what to fail with, if anything.
	err error
}

func (f *fakeSender) Send(_ context.Context, params mailkube.SendEmailParams) (*mailkube.Email, error) {
	f.calls++
	f.last = params

	if f.err != nil {
		return nil, f.err
	}
	if f.email != nil {
		return f.email, nil
	}
	return &mailkube.Email{
		ID:        "9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77",
		MessageID: "<9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77@msg.mailkube.com>",
	}, nil
}

// result is one `emails send` run, captured whole.
type result struct {
	out    string
	errOut string
	code   errs.Code
}

// send runs `emails send` against a fake server and buffered streams.
//
// It goes through the feature's own command rather than calling a runner, so the flag parsing,
// the merging and the view rendering are all exercised — which is where most of this feature
// lives. The API key is supplied through the environment because every real send needs one and
// stating it in each test would say nothing.
func send(t *testing.T, sender *fakeSender, args ...string) result {
	t.Helper()

	opts := testsupport.TestOptions{
		Env:     map[string]string{settings.EnvAPIKey: "mk_test"},
		Globals: &settings.Globals{Output: "text"},
	}
	return sendWith(t, sender, opts, args...)
}

// sendWith runs `emails send` with the dependencies a test chose.
func sendWith(t *testing.T, sender *fakeSender, opts testsupport.TestOptions, args ...string) result {
	t.Helper()

	deps, out, errOut := testsupport.TestDeps(t, opts)

	f := emails.New()
	if sender != nil {
		f.Sender = func(*feature.Deps, settings.Resolved) (ports.EmailSender, error) { return sender, nil }
	}

	cmd := f.Command(deps)
	cmd.SetArgs(append([]string{"send"}, args...))
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	code := errs.CodeOK
	if err := cmd.Execute(); err != nil {
		// Rendered the way the composition root renders it, rather than as the bare message,
		// so a test asserting on what a user reads is asserting on what a user reads: the
		// hints and the retry note only exist in the rendered form.
		detail := errs.Describe(err)
		code = detail.Code
		for _, line := range errs.Render(detail, deps.Caps.Glyphs.Cross) {
			errOut.WriteString(line + "\n")
		}
	}
	return result{out: out.String(), errOut: errOut.String(), code: code}
}

// baseArgs are a valid minimal send, for the tests whose subject is one flag added to it.
func baseArgs(extra ...string) []string {
	return append([]string{
		"--from", "hello@acme.com",
		"--to", "alice@example.com",
		"--subject", "Welcome",
		"--text", "Thanks for signing up.",
	}, extra...)
}
