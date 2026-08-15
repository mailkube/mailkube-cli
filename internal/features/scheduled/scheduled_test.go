package scheduled_test

import (
	"errors"
	"strings"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

func TestAListingShowsWhereInTheCollectionItIs(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.page = samplePage()

	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "list")
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}

	// Ids are abbreviated in a table and shown in full everywhere a user copies one.
	if !strings.Contains(got.out, "3a1f9c2d") {
		t.Errorf("the listing does not show the abbreviated id:\n%s", got.out)
	}
	if strings.Contains(got.out, "3a1f9c2d-7d8e") {
		t.Errorf("the listing shows a full id, which the column cannot hold:\n%s", got.out)
	}
	// Without the position, a first page of a long collection looks like the whole thing.
	if !strings.Contains(got.out, "of 51") || !strings.Contains(got.out, "--all") {
		t.Errorf("the listing does not say what it is a page of:\n%s", got.out)
	}
}

func TestAnEmptyListingSaysSoRatherThanPrintingAHeader(t *testing.T) {
	t.Parallel()

	got := runCmd(t, newFakeSchedules(), nil, testsupport.TestOptions{}, "list")
	if !strings.Contains(got.out, "No scheduled sends match.") {
		t.Errorf("an empty listing rendered as a bare table:\n%s", got.out)
	}
}

func TestTheFiltersReachTheQuery(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	got := runCmd(t, schedules, nil, testsupport.TestOptions{},
		"list", "--status", "scheduled", "--batch-id", "digest-w33", "--after", "+1h", "--page", "2")

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if schedules.listed.BatchID != "digest-w33" {
		t.Errorf("batch filter = %q, want digest-w33", schedules.listed.BatchID)
	}
	if len(schedules.listed.Status) != 1 || schedules.listed.Status[0] != "scheduled" {
		t.Errorf("status filter = %v, want [scheduled]", schedules.listed.Status)
	}
	if schedules.listed.Page != 2 {
		t.Errorf("page = %d, want 2", schedules.listed.Page)
	}
	if schedules.listed.ScheduledAtGTE.IsZero() {
		t.Error("--after did not reach the query")
	}
}

func TestListingBySentIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "list", "--status", "sent")

	// Passing it through would return an empty page, which reads as "nothing was sent" — the
	// opposite of the truth. The refusal says where the answer actually lives.
	if got.code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}
	if !strings.Contains(got.errOut, "webhook") {
		t.Errorf("the refusal does not point at where delivery outcomes are:\n%s", got.errOut)
	}
	if schedules.calls["list"] != 0 {
		t.Error("a refused filter still reached the server")
	}
}

func TestAnUnknownStatusNamesTheOnesThatWork(t *testing.T) {
	t.Parallel()

	got := runCmd(t, newFakeSchedules(), nil, testsupport.TestOptions{}, "list", "--status", "pending")
	if got.code != errs.CodeUsage {
		t.Fatalf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}
	if !strings.Contains(got.errOut, "scheduled") {
		t.Errorf("the refusal does not list the usable statuses:\n%s", got.errOut)
	}
}

func TestPagingByHandAndWalkingEverythingAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	got := runCmd(t, newFakeSchedules(), nil, testsupport.TestOptions{}, "list", "--all", "--page", "2")
	if got.code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}
}

func TestMaxItemsWithoutAllIsRefused(t *testing.T) {
	t.Parallel()

	// It stops the --all walk client-side and sends no query parameter, so on a single page
	// it would do nothing at all — and silently doing nothing is how someone concludes the
	// server ignored their filter.
	got := runCmd(t, newFakeSchedules(), nil, testsupport.TestOptions{}, "list", "--max-items", "5")
	if got.code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}
}

func TestWalkingEverythingStopsAtTheCallersCapAndSaysSo(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.items = samplePage().Data

	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "list", "--all", "--max-items", "2")
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if strings.Contains(got.out, "9c04a1e7") {
		t.Errorf("--max-items 2 returned a third item:\n%s", got.out)
	}
	// A listing that stopped early and looked complete is how someone concludes a batch is
	// smaller than it is.
	if !strings.Contains(got.errOut, "max-items") {
		t.Errorf("the truncation was silent:\n%s", got.errOut)
	}
}

func TestGetShowsTheFullIdAndHowFarAwayItIs(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.email = &mailkube.ScheduledEmail{
		ID: "3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77", Status: "scheduled",
		ScheduledAt: "2026-08-14T09:32:00Z", Subject: "Reminder", Recipients: "alice@example.com",
		Tags: []mailkube.Tag{{Name: "campaign", Value: "launch"}},
	}

	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "get", "3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77")
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if !strings.Contains(got.out, "3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77") {
		t.Errorf("a detail view abbreviated the id:\n%s", got.out)
	}
	if !strings.Contains(got.out, "campaign=launch") {
		t.Errorf("the tags are missing:\n%s", got.out)
	}
}

func TestReschedulingNeedsATimeAndSendsIt(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	bare := runCmd(t, schedules, nil, testsupport.TestOptions{}, "update", "3a1f9c2d")
	if bare.code != errs.CodeUsage {
		t.Errorf("update with no --at exited %d, want %d", bare.code, errs.CodeUsage)
	}
	if schedules.calls["update"] != 0 {
		t.Error("an update with no new time still reached the server")
	}

	got := runCmd(t, schedules, nil, testsupport.TestOptions{},
		"update", "3a1f9c2d", "--at", "+2h", "--batch-id", "digest-w34")
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if schedules.updated.ScheduledAt.IsZero() {
		t.Error("no new time reached the server")
	}
	if schedules.updated.BatchID != "digest-w34" {
		t.Errorf("batch = %q, want digest-w34", schedules.updated.BatchID)
	}
}

func TestRescheduleIsAnAliasForUpdate(t *testing.T) {
	t.Parallel()

	// The verb people reach for and the verb the API uses are different words for one action,
	// and a user who guesses the first should not be told it does not exist.
	schedules := newFakeSchedules()
	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "reschedule", "3a1f9c2d", "--at", "+2h")

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if schedules.calls["update"] != 1 {
		t.Error("the alias did not reach the update verb")
	}
}

func TestCancellingIsConfirmedBeforeAnythingHappens(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	declined := runCmd(t, schedules, nil, testsupport.TestOptions{Stdin: "n\n", Caps: interactive()}, "cancel", "3a1f9c2d")

	if schedules.calls["cancel"] != 0 {
		t.Error("a declined confirmation still cancelled the send")
	}
	// Not exit 0: a script reading success here would go on believing the send is cancelled.
	if declined.code == errs.CodeOK {
		t.Error("declining reported success")
	}

	confirmed := runCmd(t, schedules, nil, testsupport.TestOptions{Stdin: "y\n", Caps: interactive()}, "cancel", "3a1f9c2d")
	if confirmed.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", confirmed.code, confirmed.errOut)
	}
	if schedules.calls["cancel"] != 1 {
		t.Error("a confirmed cancellation did not reach the server")
	}
}

func TestCancellingNonInteractivelyNeedsTheFlag(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	// No terminal and no -y: the command must refuse rather than guess, and must not hang.
	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "cancel", "3a1f9c2d")

	if got.code == errs.CodeOK {
		t.Error("a destructive command proceeded with nobody to ask")
	}
	if schedules.calls["cancel"] != 0 {
		t.Error("an unconfirmed cancellation reached the server")
	}

	assumed := runCmd(t, schedules, nil, testsupport.TestOptions{
		Globals: &settings.Globals{Output: "text", AssumeYes: true},
	}, "cancel", "3a1f9c2d")
	if assumed.code != errs.CodeOK {
		t.Fatalf("-y did not authorise the cancellation: %s", assumed.errOut)
	}
}

func TestABatchCancellationCountsBeforeItAsks(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.page = &mailkube.ScheduledEmailPage{
		Pagination: mailkube.Pagination{TotalCount: 49},
	}
	batches := newFakeBatches()
	batches.canceled = 49

	got := runCmd(t, schedules, batches, testsupport.TestOptions{Stdin: "y\n", Caps: interactive()},
		"batches", "cancel", "digest-w33")
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}

	// The question names how many messages are at stake rather than asking about a label,
	// which is the difference between an informed answer and a guess.
	if !strings.Contains(got.errOut, "49") {
		t.Errorf("the prompt did not say how many were affected:\n%s", got.errOut)
	}
	if schedules.listed.BatchID != "digest-w33" {
		t.Error("the count was not scoped to the batch")
	}
	if !strings.Contains(got.out, "49") {
		t.Errorf("the result does not report the count:\n%s", got.out)
	}
}

func TestDecliningABatchCancellationCostsOneReadAndCancelsNothing(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.page = &mailkube.ScheduledEmailPage{Pagination: mailkube.Pagination{TotalCount: 49}}
	batches := newFakeBatches()

	runCmd(t, schedules, batches, testsupport.TestOptions{Stdin: "n\n", Caps: interactive()}, "batches", "cancel", "digest-w33")

	if batches.calls["cancel"] != 0 {
		t.Error("a declined batch cancellation still ran")
	}
	if schedules.calls["list"] != 1 {
		t.Errorf("the count cost %d reads, want exactly one", schedules.calls["list"])
	}
}

func TestAnUnknownBatchIsANoOpRatherThanAnError(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.page = &mailkube.ScheduledEmailPage{}
	batches := newFakeBatches()

	got := runCmd(t, schedules, batches, testsupport.TestOptions{Stdin: "y\n", Caps: interactive()},
		"batches", "cancel", "no-such-batch")

	// The server reports zero rather than failing, and showing that is more useful than
	// inventing an error: the user learns the label matched nothing.
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if !strings.Contains(got.out, "0 emails") {
		t.Errorf("the no-op was not reported as one:\n%s", got.out)
	}
}

func TestReschedulingABatchReportsTheNewTime(t *testing.T) {
	t.Parallel()

	batches := newFakeBatches()
	batches.rescheded = 49

	got := runCmd(t, newFakeSchedules(), batches, testsupport.TestOptions{},
		"batches", "update", "digest-w33", "--at", "+2h")
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if !strings.Contains(got.out, "49") || !strings.Contains(got.out, "due ") {
		t.Errorf("the acknowledgement does not report the count and the new time:\n%s", got.out)
	}
}

func TestAMissingCredentialIsReportedBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	got := runCmd(t, schedules, nil, testsupport.TestOptions{
		Env: map[string]string{}, Globals: &settings.Globals{Output: "text"},
	}, "list")

	if got.code != errs.CodeAuth {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeAuth)
	}
	if schedules.calls["list"] != 0 {
		t.Error("a request was built with no credential")
	}
}

func TestAServerFailureKeepsItsCategory(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.err = &mailkube.APIError{
		ErrorName:  mailkube.ErrorNameScheduledEmailNotFound,
		Message:    "No scheduled email with that id.",
		StatusCode: 404,
	}

	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "get", "3a1f9c2d")
	if got.code != errs.CodeNotFound {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeNotFound)
	}
	if !strings.Contains(got.errOut, "No scheduled email with that id.") {
		t.Errorf("the server's own message was not carried through:\n%s", got.errOut)
	}
}

func TestAFailureWhileWalkingPagesIsReportedNotSwallowed(t *testing.T) {
	t.Parallel()

	schedules := newFakeSchedules()
	schedules.err = errors.New("connection reset")

	got := runCmd(t, schedules, nil, testsupport.TestOptions{}, "list", "--all")
	if got.code == errs.CodeOK {
		t.Error("a failure mid-walk was reported as a successful empty listing")
	}
}
