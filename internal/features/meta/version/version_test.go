package version_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/meta/version"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// run executes just this feature's command, which is what a feature test should do: the wiring
// above it has its own tests, and reaching for the whole tree here would test that twice.
func run(t *testing.T, args ...string) (out, errOut string, err error) {
	t.Helper()

	deps, outBuf, errBuf := testsupport.TestDeps(t, testsupport.TestOptions{})
	cmd := version.New().Command(deps)
	cmd.SetArgs(args)
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)

	err = cmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestVersionReportsTheCLITheSDKAndTheToolchain(t *testing.T) {
	t.Parallel()

	out, errOut, err := run(t)
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	// The streams here are buffers, so the format is inferred as JSON. Asserting on the fields
	// rather than on rendered text is the right level: the shape is the contract, the wording
	// is not.
	var got version.View
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got.Version == "" || got.SDKVersion == "" || got.GoVersion == "" {
		t.Errorf("a version field is empty: %+v", got)
	}
	if errOut != "" {
		t.Errorf("a successful command wrote to stderr: %q", errOut)
	}
}

func TestTheJSONFlagIsTheSameEncodingAsTheGlobalOne(t *testing.T) {
	t.Parallel()

	// --json is a natural spelling on this one command, not a second encoding path: it sets the
	// resolved format, so the two cannot drift.
	withFlag, _, err := run(t, "--json")
	if err != nil {
		t.Fatalf("version --json: %v", err)
	}
	inferred, _, err := run(t)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if withFlag != inferred {
		t.Errorf("--json produced different output:\n%s\n%s", withFlag, inferred)
	}
}

func TestTheTextRenderingNamesAllThree(t *testing.T) {
	t.Parallel()

	lines := version.View{Version: "v1.2.3", SDKVersion: "v1.1.0", GoVersion: "go1.24.0"}.
		RenderText(output.Caps{Glyphs: output.ASCIIGlyphs()})

	rendered := strings.Join(lines, "\n")
	for _, want := range []string{"mailkube v1.2.3", "mailkube-go v1.1.0", "go1.24.0"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendering is missing %q:\n%s", want, rendered)
		}
	}
}

func TestVersionMakesNoNetworkCall(t *testing.T) {
	t.Parallel()

	// There is no update check here and none anywhere else by default. A tool that contacts a
	// release server every time it runs is a privacy problem and breaks in air-gapped CI, so
	// this asserts the absence rather than trusting it: the command has no --check yet, and
	// when it gains one it must stay opt-in.
	cmd := version.New().Command(nil)
	if cmd.Flags().Lookup("check") != nil {
		t.Error("version grew a --check flag; make sure it is opt-in and update this test")
	}
}
