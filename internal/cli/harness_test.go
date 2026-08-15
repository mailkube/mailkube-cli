package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/golden"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// result is one command run, captured whole.
type result struct {
	out    string
	errOut string
	code   errs.Code
}

// run executes the real command tree against buffers and a temporary config file.
//
// It goes through NewRootCmd rather than calling a feature's runner, because half of what these
// tests are checking lives in the wiring: the global flags, the format resolution, and the
// stream split are all decided outside any one command.
func run(t *testing.T, opts testsupport.TestOptions, args ...string) result {
	t.Helper()

	deps, out, errOut := testsupport.TestDeps(t, opts)
	code := cli.Run(t.Context(), deps, args)
	return result{out: out.String(), errOut: errOut.String(), code: errs.Code(code)}
}

// configured is a config file with both credentials in it, written through the CLI itself.
//
// Building the fixture by running the commands rather than by writing TOML is deliberate: it
// means the tests that read a configured profile are reading one this program actually produces,
// so a change to the file's shape cannot leave the fixtures describing a format nothing writes.
func configured(t *testing.T) (path string, env map[string]string) {
	t.Helper()

	path = filepath.Join(t.TempDir(), "mailkube", "config.toml")
	opts := testsupport.TestOptions{ConfigPath: path}

	if got := run(t, opts, "auth", "login", "--key", "mk_j3k1a2b3c4d5f8a2", "--no-verify"); got.code != errs.CodeOK {
		t.Fatalf("storing the API key: %s", got.errOut)
	}
	if got := run(t, opts, "config", "set", "smtp_user", "app01@acme.com"); got.code != errs.CodeOK {
		t.Fatalf("storing the SMTP username: %s", got.errOut)
	}
	return path, map[string]string{}
}

// assertGolden compares both streams against committed files, and asserts the stream contract.
//
// stdout is checked for emptiness on failure as a blanket rule rather than per test, because it
// is the one guarantee every scripted caller depends on and the one a new command is most likely
// to break without noticing.
//
// verdict opts a case out of that check, and only `doctor` uses it. Its exit code reports on the
// content of a document that is always written in full, the way `grep` and `diff` answer, rather
// than reporting a failure to produce one. The rule exists so a caller piping into a parser never
// sees half a document; that reasoning does not reach a document that is always whole. Opting out
// is a per-case decision so the exception stays visible at the point it is taken.
func assertGolden(t *testing.T, name string, got result, verdict bool) {
	t.Helper()

	golden.Assert(t, name+".out", []byte(got.out))
	golden.Assert(t, name+".err", []byte(got.errOut))

	if got.code != errs.CodeOK && !verdict && strings.TrimSpace(got.out) != "" {
		t.Errorf("a failing command wrote to stdout, which breaks the payload contract:\n%s", got.out)
	}
}

// writeFile creates a file and the directory above it, for the fixtures a test hand-builds.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// readFile returns a file's contents, failing the test if it cannot.
func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}

// errorMessage decodes the message out of the JSON error envelope on the error stream.
//
// Decoding rather than searching the raw text: the envelope is JSON, so a value containing a
// backslash arrives escaped, and a path spelled the way this platform spells it appears nowhere
// in the stream. Asserting on the decoded field is also the level a caller works at.
func errorMessage(t *testing.T, errOut string) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(errOut), &envelope); err != nil {
		t.Fatalf("decoding the error envelope %q: %v", errOut, err)
	}
	return envelope.Error.Message
}
