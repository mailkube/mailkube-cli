package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

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
	deps := &feature.Deps{
		IO:   &feature.IOStreams{ErrOut: &errOut},
		Caps: output.Caps{Glyphs: output.UnicodeGlyphs()},
	}

	code := report(deps, errs.WithCode(errs.CodeNotFound, errors.New("no such scheduled email")))

	if code != int(errs.CodeNotFound) {
		t.Errorf("exit code = %d, want %d", code, errs.CodeNotFound)
	}
	if got, want := errOut.String(), "✗ no such scheduled email\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}
