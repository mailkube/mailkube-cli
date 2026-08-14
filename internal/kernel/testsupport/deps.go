// Package testsupport builds the dependencies a test runs a command against.
//
// It lives outside the kernel packages it constructs so that nothing in the shipping binary
// imports the testing package, and so there is exactly one definition of what a deterministic
// run looks like: a fixed clock, a fixed terminal width, no colour, and a config file inside a
// temporary directory. A test that assembled its own would eventually differ from the others in
// some detail nobody chose, and the golden files would record that difference as behaviour.
package testsupport

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// TestOptions adjust the dependencies a test runs a command against.
//
// Every field has a deterministic zero value, so a test states only what it cares about. That is
// the property that keeps golden files stable: a screen changes when the code changes, never
// because a test forgot to pin the terminal width or the clock.
type TestOptions struct {
	// Stdin is what the command reads, for prompts and the @- convention.
	Stdin string
	// Env is the process environment the command resolves settings from.
	Env map[string]string
	// Caps overrides the terminal model. The zero value is a non-terminal: ASCII glyphs
	// would be wrong to assume, so the default is the Unicode set at a fixed width.
	Caps *output.Caps
	// ConfigPath is where the config file lives. Empty means a file inside t.TempDir(), so a
	// test can never read or write the developer's real configuration.
	ConfigPath string
	// Globals are the parsed global flags, as if they had come from the command line.
	Globals *settings.Globals
}

// TestDeps builds dependencies over buffers and a temporary config file, and prepares them.
//
// It returns the two output buffers separately rather than exposing them through the struct, so
// a test cannot accidentally assert on the wrong stream — which is the one mistake that would
// let the stdout/stderr contract break while every test stayed green.
func TestDeps(t testing.TB, opts TestOptions) (deps *feature.Deps, out, errOut *bytes.Buffer) {
	t.Helper()

	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	streams := &feature.IOStreams{In: stdin(opts.Stdin), Out: out, ErrOut: errOut}

	globals := opts.Globals
	if globals == nil {
		globals = &settings.Globals{}
	}
	// The config path is supplied as the flag rather than by handing over a ready-made store,
	// so the resolution a test exercises is the one the program really performs. The exception
	// is a test that sets the environment variable on purpose: leaving the flag unset there is
	// what lets it observe the variable winning. Every other case gets a path inside t.TempDir,
	// which is what guarantees no test can read or write the developer's own configuration.
	if globals.ConfigPath == "" && opts.Env[settings.EnvConfig] == "" {
		globals.ConfigPath = opts.ConfigPath
		if globals.ConfigPath == "" {
			globals.ConfigPath = filepath.Join(t.TempDir(), "mailkube", "config.toml")
		}
	}

	deps = &feature.Deps{
		IO:      streams,
		Caps:    testCaps(opts.Caps),
		Clock:   clock.Testing(),
		Env:     output.MapEnv(opts.Env),
		Globals: globals,
	}
	if err := deps.Prepare(); err != nil {
		t.Fatalf("preparing test dependencies: %v", err)
	}
	return deps, out, errOut
}

// stdin turns a string into a readable stream, including the empty case.
func stdin(s string) io.Reader { return bytes.NewBufferString(s) }

// testCaps is the fixed terminal model golden files are rendered against.
//
// Width is pinned because a column layout that depended on the developer's window would produce
// a different golden on every machine. Colour is off because ANSI in a committed file makes the
// diff unreadable, which defeats the point of committing it.
func testCaps(override *output.Caps) output.Caps {
	if override != nil {
		return *override
	}
	return output.Caps{
		TTY:     false,
		Color:   false,
		Unicode: true,
		Width:   output.DefaultWidth,
		Glyphs:  output.UnicodeGlyphs(),
	}
}
