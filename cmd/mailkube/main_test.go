package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
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

	code := run([]string{"definitely-not-a-command"}, strings.NewReader(""), &out, &errOut)

	if code != int(errs.CodeUsage) {
		t.Errorf("exit code = %d, want %d for an unknown command", code, errs.CodeUsage)
	}
	if out.Len() != 0 {
		t.Errorf("stdout must be empty on failure, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "✗") {
		t.Errorf("stderr does not carry the error: %q", errOut.String())
	}
}

// An unparseable command line is the caller's mistake, not the server's. Before root owned its
// own arguments, cobra rejected an unknown command with an uncategorised error that landed on the
// "server error" default — telling the user a typo might be worth retrying.
func TestRunExitsTwoForAMalformedCommandLine(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown command", []string{"definitely-not-a-command"}},
		{"unknown flag", []string{"--definitely-not-a-flag"}},
		{"unknown flag on a subcommand", []string{"version", "--definitely-not-a-flag"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tc.args, strings.NewReader(""), &out, &errOut)
			if code != int(errs.CodeUsage) {
				t.Errorf("exit code = %d, want %d\nstderr: %s", code, errs.CodeUsage, errOut.String())
			}
			if out.Len() != 0 {
				t.Errorf("stdout must be empty on failure, got %q", out.String())
			}
		})
	}
}

func TestRunPrintsHelpWhenGivenNothing(t *testing.T) {
	var out, errOut bytes.Buffer

	if code := run(nil, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "mailkube") {
		t.Errorf("no help on stdout: %q", out.String())
	}
}

// A panic is this program's fault, so it exits 1 — distinct from every code that tells the caller
// something about their request — and it still leaves stdout clean for whatever is parsing it.
func TestReportPanicExitsOneWithATraceOnStderr(t *testing.T) {
	var errOut bytes.Buffer

	code := reportPanic(&errOut, "nil map write", []byte("goroutine 1 [running]:\nmain.boom()\n"))

	if code != int(errs.CodeInternal) {
		t.Errorf("exit code = %d, want %d", code, errs.CodeInternal)
	}
	report := errOut.String()
	for _, want := range []string{"internal error", "nil map write", issueTracker, "main.boom()"} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
}

func TestReportRendersTheDetailAndReturnsItsCode(t *testing.T) {
	var errOut bytes.Buffer

	code := report(&errOut, errs.WithCode(errs.CodeNotFound, errors.New("no such scheduled email")))

	if code != int(errs.CodeNotFound) {
		t.Errorf("exit code = %d, want %d", code, errs.CodeNotFound)
	}
	if got, want := errOut.String(), "✗ no such scheduled email\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}
