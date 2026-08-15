package topic_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/meta/topic"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// run executes one topic invocation.
func run(t *testing.T, deps *feature.Deps, args ...string) error {
	t.Helper()

	cmd := topic.New().Command(deps)
	cmd.SetArgs(testsupport.Args(args))
	cmd.SetOut(deps.IO.Out)
	cmd.SetErr(deps.IO.ErrOut)
	return cmd.Execute()
}

// TestEveryTopicTheListingOffersCanBeRead is the property that makes the listing trustworthy:
// its summaries are derived from the files themselves, so a topic cannot advertise a subject it
// no longer covers, and cannot be advertised without existing.
func TestEveryTopicTheListingOffersCanBeRead(t *testing.T) {
	t.Parallel()

	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Format = output.Text

	if err := run(t, deps); err != nil {
		t.Fatalf("topic: %v", err)
	}
	listing := out.String()

	for _, name := range []string{
		"output", "exit-codes", "idempotency", "profiles", "webhooks", "files", "testing",
	} {
		if !strings.Contains(listing, name) {
			t.Errorf("the listing does not offer %q:\n%s", name, listing)
		}

		body, errOut, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
		body.Format = output.Text
		_ = errOut
		if err := run(t, body, name); err != nil {
			t.Errorf("topic %s: %v", name, err)
		}
	}
}

func TestAnUnknownTopicNamesTheRealOnes(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	err := run(t, deps, "nonesuch")
	if err == nil {
		t.Fatal("an unknown topic was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
	}
	// A user who mistyped one is then one line from the answer, and a user who invented one
	// can see that they did.
	if !strings.Contains(err.Error(), "exit-codes") {
		t.Errorf("the error does not list the real topics: %v", err)
	}
}

func TestTheExitCodeTopicDocumentsEveryCodeTheProgramProduces(t *testing.T) {
	t.Parallel()

	// The codes are a semver-stable contract that scripts branch on, so the documentation of
	// them is asserted against the program rather than maintained beside it and hoped over.
	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Format = output.Text
	if err := run(t, deps, "exit-codes"); err != nil {
		t.Fatalf("topic exit-codes: %v", err)
	}
	documented := out.String()

	for _, code := range []errs.Code{
		errs.CodeOK, errs.CodeInternal, errs.CodeUsage, errs.CodeAuth, errs.CodeValidation,
		errs.CodeConfig, errs.CodeNotFound, errs.CodeRateLimit, errs.CodeServer,
		errs.CodeNetwork, errs.CodePartial, errs.CodeDeadline, errs.CodeInterrupt,
	} {
		if !strings.Contains(documented, itoa(int(code))) {
			t.Errorf("exit code %d is not documented:\n%s", code, documented)
		}
	}
}

// itoa avoids pulling strconv in for one call in one test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestTheCompletionOffersTheTopicNames(t *testing.T) {
	t.Parallel()

	// The whole point of the listing is that a user does not know what to type, so a shell that
	// can finish it for them is better than one that makes them run the listing first.
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	cmd := topic.New().Command(deps)

	names, _ := cmd.ValidArgsFunction(cmd, nil, "")
	if len(names) == 0 {
		t.Fatal("completion offers no topics")
	}
	found := false
	for _, name := range names {
		if name == "webhooks" {
			found = true
		}
	}
	if !found {
		t.Errorf("completion does not offer the webhooks topic: %v", names)
	}
}
