package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunReturnsZeroAndWritesThePayloadToStdout(t *testing.T) {
	var out, errOut bytes.Buffer

	if code := run([]string{"version"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "mailkube") {
		t.Errorf("stdout = %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("a successful run wrote to stderr: %q", errOut.String())
	}
}

func TestRunReportsAFailureOnStderrAndLeavesStdoutEmpty(t *testing.T) {
	// The property a script depends on: a failed command produces no payload, so piping into
	// a parser never yields a partial document.
	var out, errOut bytes.Buffer

	if code := run([]string{"definitely-not-a-command"}, strings.NewReader(""), &out, &errOut); code == 0 {
		t.Fatal("exit code = 0 for an unknown command")
	}
	if out.Len() != 0 {
		t.Errorf("stdout must be empty on failure, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "✗") {
		t.Errorf("stderr does not carry the error: %q", errOut.String())
	}
}
