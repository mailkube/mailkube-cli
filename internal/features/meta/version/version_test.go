package version_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// run executes the command tree with the given arguments and returns both streams.
func run(t *testing.T, args ...string) (out, errOut string, err error) {
	t.Helper()
	streams, outBuf, errBuf := feature.TestStreams()
	root := cli.NewRootCmd(&feature.Deps{IO: streams})
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestVersionReportsTheCLITheSDKAndTheToolchain(t *testing.T) {
	out, errOut, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	for _, want := range []string{"mailkube ", "sdk  mailkube-go ", "go   "} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	if errOut != "" {
		t.Errorf("a successful command wrote to stderr: %q", errOut)
	}
}

func TestVersionJSONIsParseableAndCarriesEveryField(t *testing.T) {
	// The JSON shape is a contract: scripts branch on it, so it is asserted as data rather
	// than as text.
	out, _, err := run(t, "version", "--json")
	if err != nil {
		t.Fatalf("version --json: %v", err)
	}

	var got struct {
		Version    string `json:"Version"`
		SDKVersion string `json:"SDKVersion"`
		GoVersion  string `json:"GoVersion"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got.Version == "" || got.SDKVersion == "" || got.GoVersion == "" {
		t.Errorf("a field is empty: %+v", got)
	}
}

func TestTheSuccessPayloadNeverGoesToStderr(t *testing.T) {
	// The stream split is the contract every script depends on, so it is asserted directly
	// rather than left to golden files, which would stay green if the two were swapped.
	out, errOut, err := run(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if out == "" {
		t.Error("stdout is empty; the payload must go there")
	}
	if errOut != "" {
		t.Errorf("stderr must be empty on success, got %q", errOut)
	}
}

func TestAnUnknownCommandFailsWithoutWritingAPayload(t *testing.T) {
	// On failure stdout stays empty, so `mailkube ... | jq` never sees half a document.
	out, _, err := run(t, "definitely-not-a-command")
	if err == nil {
		t.Fatal("an unknown command must be an error")
	}
	if out != "" {
		t.Errorf("stdout must be empty on failure, got %q", out)
	}
}
