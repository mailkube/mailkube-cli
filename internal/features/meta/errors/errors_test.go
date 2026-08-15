package errors_test

import (
	"strings"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	mkerrors "github.com/mailkube/mailkube-cli/internal/features/meta/errors"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// explain runs `errors explain` against buffered streams.
func explain(t *testing.T, args ...string) (out, errOut string, code errs.Code) {
	t.Helper()

	deps, outBuf, errBuf := testsupport.TestDeps(t, testsupport.TestOptions{
		Globals: &settings.Globals{Output: "text"},
	})

	cmd := mkerrors.New().Command(deps)
	cmd.SetArgs(append([]string{"explain"}, args...))
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	code = errs.CodeOK
	if err := cmd.Execute(); err != nil {
		detail := errs.Describe(err)
		code = detail.Code
		errBuf.WriteString(strings.Join(errs.Render(detail, "x"), "\n"))
	}
	return outBuf.String(), errBuf.String(), code
}

func TestAnExplanationSaysWhatToDoAndWhetherRetryingHelps(t *testing.T) {
	t.Parallel()

	out, _, code := explain(t, mailkube.ErrorNameQuotaExceeded)
	if code != errs.CodeOK {
		t.Fatalf("exit code = %d", code)
	}

	// The three things a reader came for, in the order they need them.
	for _, want := range []string{"quota_exceeded", "HTTP 422", "not retryable", "Fix"} {
		if !strings.Contains(out, want) {
			t.Errorf("the explanation does not mention %q:\n%s", want, out)
		}
	}
	// The surprising, true thing. Someone whose quota vanished faster than their send count
	// explains has no other way to learn this.
	if !strings.Contains(out, "Suppressed recipients still consume quota") {
		t.Errorf("the note is missing:\n%s", out)
	}
}

func TestAPlanEntitlementIsNotDescribedAsACredentialProblem(t *testing.T) {
	t.Parallel()

	// scheduling_not_included is a 403 that has nothing to do with the key. Reporting it as
	// authentication sends a human to re-check a credential that was fine.
	out, _, _ := explain(t, mailkube.ErrorNameSchedulingNotIncluded)
	if !strings.Contains(out, "plan entitlement") {
		t.Errorf("the explanation does not distinguish it from an auth failure:\n%s", out)
	}
}

func TestAnSMTPCodeIsFoundByItsCodeAndByThePairAQuoteCarries(t *testing.T) {
	t.Parallel()

	byCode, _, code := explain(t, "535")
	if code != errs.CodeOK {
		t.Fatalf("exit code = %d", code)
	}
	// The enhanced status is part of the identity, and it is the pair a bounce quotes.
	if !strings.Contains(byCode, "535 5.7.8") {
		t.Errorf("the heading omits the enhanced status:\n%s", byCode)
	}

	byPair, _, code := explain(t, "535 5.7.8")
	if code != errs.CodeOK {
		t.Fatalf("looking up the quoted pair: exit code = %d", code)
	}
	if byPair != byCode {
		t.Error("the code and the quoted pair found different entries")
	}
}

func TestTheLookupForgivesTheShapesANameIsWrittenIn(t *testing.T) {
	t.Parallel()

	// Someone pasting from a log has quota_exceeded; someone reading prose has "quota
	// exceeded". Turning the second away on punctuation makes the command feel broken at the
	// moment it is most needed.
	fromLog, _, _ := explain(t, "quota_exceeded")
	fromProse, _, code := explain(t, "quota exceeded")

	if code != errs.CodeOK || fromProse != fromLog {
		t.Errorf("a spaced spelling did not resolve to the same entry:\n%s", fromProse)
	}
}

func TestAnUnknownNameIsRefusedRatherThanInvented(t *testing.T) {
	t.Parallel()

	out, errOut, code := explain(t, "definitely_not_a_real_error")
	if code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", code, errs.CodeUsage)
	}
	if out != "" {
		t.Errorf("a failed lookup wrote to the payload stream:\n%s", out)
	}
	// A confident wrong answer about a failure is worse than none, so the refusal points at
	// the list instead.
	if !strings.Contains(errOut, "--list") {
		t.Errorf("the refusal does not point at the catalogue:\n%s", errOut)
	}
}

func TestTheCatalogueListsBothHalves(t *testing.T) {
	t.Parallel()

	out, _, code := explain(t, "--list")
	if code != errs.CodeOK {
		t.Fatalf("exit code = %d", code)
	}
	for _, want := range []string{"API error names:", "SMTP reply codes:", "quota_exceeded", "535"} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not mention %q:\n%s", want, out)
		}
	}
}

func TestExplainWithNoArgumentListsRatherThanFailing(t *testing.T) {
	t.Parallel()

	// Someone who does not know a name yet is exactly the person this command is for.
	out, _, code := explain(t)
	if code != errs.CodeOK || !strings.Contains(out, "API error names:") {
		t.Errorf("a bare `errors explain` did not list the catalogue:\n%s", out)
	}
}

func TestNoExplanationIsWiderThanTheTerminal(t *testing.T) {
	t.Parallel()

	// Prose is wrapped to the resolved width rather than left to the terminal, because a
	// hanging indent under "Check" only reads as one if it stays under it.
	out, _, _ := explain(t, mailkube.ErrorNameQuotaExceeded)
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 80 {
			t.Errorf("a line overflows the width: %q", line)
		}
	}
}
