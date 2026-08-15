package webhooks_test

import (
	"io"
	"net"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/webhooks"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// refuse runs `webhooks listen` expecting it to stop before it opens a socket.
//
// The bind seam fails the test if it is reached, which makes "every refusal happens before a
// socket is opened" an assertion rather than a claim. A listener that binds, prints a banner and
// only then discovers it cannot work has already told the user it is working.
func refuse(t *testing.T, opts testsupport.TestOptions, args ...string) (errs.Code, string) {
	t.Helper()

	if opts.Globals == nil {
		opts.Globals = &settings.Globals{Output: "text"}
	}
	deps, out, errOut := testsupport.TestDeps(t, opts)

	f := webhooks.New()
	f.Bind = func(address string) (net.Listener, error) {
		t.Errorf("a refused run opened a socket on %s", address)
		return nil, nil
	}

	cmd := f.Command(deps)
	cmd.SetArgs(append([]string{"listen"}, args...))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.ExecuteContext(t.Context())
	if err == nil {
		t.Fatalf("the run was not refused; stdout was %q", out)
	}
	detail := errs.Describe(err)
	return detail.Code, strings.Join(errs.Render(detail, deps.Caps.Glyphs.Cross), "\n") + errOut.String()
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	// Each case builds its own options rather than sharing one value. TestDeps completes the
	// globals it is given, so a shared struct would be written by every parallel subtest at
	// once, and the suite would be racing on its own fixture rather than on the code.
	withSecret := running

	tests := []struct {
		name string
		opts func() testsupport.TestOptions
		args []string
		code errs.Code
		says string
	}{
		{
			// The posture, not an oversight: an endpoint URL is public and anyone can post
			// to it, so the signature is the only thing separating a real delivery from a
			// stranger's. A tool whose default skipped it would teach that habit.
			name: "no secret at all",
			opts: func() testsupport.TestOptions {
				return testsupport.TestOptions{Globals: &settings.Globals{Output: "text"}}
			},
			args: []string{"--public-url", "https://a1b2.example.com"},
			code: errs.CodeConfig,
			says: "no signing secret",
		},
		{
			name: "a secret and --skip-verify together",
			opts: withSecret,
			args: []string{"--public-url", "https://a1b2.example.com", "--skip-verify"},
			code: errs.CodeUsage,
			says: "opposite things",
		},
		{
			// Without a public address no event can ever arrive, so a listener that
			// started anyway would sit there looking correct.
			name: "no public url",
			opts: withSecret,
			args: nil,
			code: errs.CodeConfig,
			says: "a public URL is required",
		},
		{
			name: "a public url that is not https",
			opts: withSecret,
			args: []string{"--public-url", "http://a1b2.example.com"},
			code: errs.CodeConfig,
			says: "not an https URL",
		},
		{
			name: "a public url that is not a url",
			opts: withSecret,
			args: []string{"--public-url", "not a url"},
			code: errs.CodeConfig,
			says: "is not a URL",
		},
		{
			name: "an unusable port",
			opts: withSecret,
			args: []string{"--public-url", "https://a1b2.example.com", "--port", "70000"},
			code: errs.CodeUsage,
			says: "not a usable port",
		},
		{
			name: "a path that is not one",
			opts: withSecret,
			args: []string{"--public-url", "https://a1b2.example.com", "--path", "hooks"},
			code: errs.CodeUsage,
			says: "must begin with /",
		},
		{
			name: "a size that is not one",
			opts: withSecret,
			args: []string{"--public-url", "https://a1b2.example.com", "--max-body", "lots"},
			code: errs.CodeUsage,
			says: "not a usable size",
		},
		{
			name: "a field width nothing fits in",
			opts: withSecret,
			args: []string{"--public-url", "https://a1b2.example.com", "--max-field", "4"},
			code: errs.CodeUsage,
			says: "--max-field must be at least",
		},
		{
			name: "an unknown print mode",
			opts: withSecret,
			args: []string{"--public-url", "https://a1b2.example.com", "--print", "loud"},
			code: errs.CodeUsage,
			says: "unknown --print",
		},
		{
			// A stream has no end, and a YAML document set needs separators an encoder can
			// only write when it knows how many documents there are.
			name: "a format with no line-oriented form",
			opts: yamlOutput,
			args: []string{"--public-url", "https://a1b2.example.com"},
			code: errs.CodeUsage,
			says: "-o ndjson",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, reported := refuse(t, tt.opts(), tt.args...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d: %s", code, tt.code, reported)
			}
			if !strings.Contains(reported, tt.says) {
				t.Errorf("the refusal does not say %q:\n%s", tt.says, reported)
			}
		})
	}
}

// yamlOutput asks for the one format a stream cannot be rendered in.
func yamlOutput() testsupport.TestOptions {
	opts := running()
	opts.Globals = &settings.Globals{Output: "yaml"}
	return opts
}

func TestABindFailureNamesThePortAndTheFlagThatChangesIt(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, running())

	f := webhooks.New()
	f.Bind = func(address string) (net.Listener, error) {
		return nil, &net.OpError{Op: "listen", Net: "tcp4", Err: errAddressInUse{}}
	}

	cmd := f.Command(deps)
	cmd.SetArgs([]string{"listen", "--public-url", "https://a1b2.example.com"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.ExecuteContext(t.Context())
	if err == nil {
		t.Fatal("a listener that could not bind reported success")
	}
	if got := errs.CodeFor(err); got != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got, errs.CodeConfig)
	}
	// The address the user can act on, and the flag that changes it. Not the Go error, which
	// repeats the address inside a sentence about sockets.
	for _, want := range []string{"127.0.0.1:4318", "address already in use", "--port"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
}

func TestTheManagementVerbsPointAtThePageThatOwnsThem(t *testing.T) {
	t.Parallel()

	// `webhooks listen` establishes the noun, which makes `webhooks create` the most likely
	// wrong guess in the whole CLI. Answering "unknown command" would tell someone the thing
	// does not exist when they are one page away from it.
	for _, verb := range []string{"create", "list", "get", "update", "delete"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()

			deps, _, _ := testsupport.TestDeps(t, running())
			f := webhooks.New()
			f.Bind = func(address string) (net.Listener, error) {
				t.Errorf("a referral opened a socket on %s", address)
				return nil, nil
			}

			cmd := f.Command(deps)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			// Cobra matches from the command it is given, so the subtree's own name is
			// dropped: this is `mailkube webhooks <verb>` as a user would type it. The
			// stray flag is there on purpose, because someone guessing at a verb guesses
			// at its flags too.
			cmd.SetArgs([]string{verb, "--json"})

			err := cmd.ExecuteContext(t.Context())
			if err == nil {
				t.Fatalf("`webhooks %s` reported success", verb)
			}
			if got := errs.CodeFor(err); got != errs.CodeUsage {
				t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
			}
			if !strings.Contains(err.Error(), "/domain/webhooks") {
				t.Errorf("the answer does not name the page that owns it: %v", err)
			}
		})
	}
}

// errAddressInUse stands in for the operating system's own refusal.
type errAddressInUse struct{}

func (errAddressInUse) Error() string { return "bind: address already in use" }
