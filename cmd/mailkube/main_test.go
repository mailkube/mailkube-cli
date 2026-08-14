package main

import (
	"bytes"
	"strings"
	"testing"
)

// These tests cover the one thing this package still owns: assembling the real dependencies. The
// exit-code mapping and the failure rendering live in internal/cli and are tested there, which is
// what keeps main small enough to read in one screen.

func TestRunInfersJSONWhenTheOutputIsNotATerminal(t *testing.T) {
	// No flag was passed, and the streams here are buffers rather than terminals. Machine
	// output without having to ask for it is the contract every script depends on.
	var out, errOut bytes.Buffer

	if code := run([]string{"version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("stdout is not JSON: %q", out.String())
	}
	if !strings.Contains(out.String(), `"sdkVersion"`) {
		t.Errorf("stdout does not carry the version payload: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("a successful run wrote to stderr: %q", errOut.String())
	}
}

func TestRunReportsAFailureOnStderrAndLeavesStdoutEmpty(t *testing.T) {
	// The property a script depends on: a failed command produces no payload, so piping into a
	// parser never yields a partial document.
	var out, errOut bytes.Buffer

	code := run([]string{"no-such-command"}, strings.NewReader(""), &out, &errOut)

	if code == 0 {
		t.Error("an unknown command succeeded")
	}
	if out.Len() != 0 {
		t.Errorf("a failed run wrote to stdout: %q", out.String())
	}
	if !strings.Contains(errOut.String(), "no-such-command") {
		t.Errorf("stderr does not name the command: %q", errOut.String())
	}
}
