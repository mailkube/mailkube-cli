package scheduled_test

import (
	"context"
	"iter"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/scheduled"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// fakeSchedules stands in for the scheduled-emails service, recording what it was asked.
type fakeSchedules struct {
	// page is what List answers with.
	page *mailkube.ScheduledEmailPage
	// items are what All yields, in order.
	items []mailkube.ScheduledEmail
	// email is what Get and Update answer with.
	email *mailkube.ScheduledEmail
	// err is what every verb fails with, if anything.
	err error

	// listed records the filters List was called with, which is where half the behaviour of
	// this feature lives: a flag that never reached the query is invisible in the output.
	listed  mailkube.ScheduledEmailListParams
	updated mailkube.ScheduledEmailUpdateParams
	// calls counts each verb, so "nothing was cancelled" is asserted rather than hoped.
	calls map[string]int
}

func newFakeSchedules() *fakeSchedules {
	return &fakeSchedules{calls: map[string]int{}}
}

func (f *fakeSchedules) List(
	_ context.Context, params mailkube.ScheduledEmailListParams,
) (*mailkube.ScheduledEmailPage, error) {
	f.calls["list"]++
	f.listed = params

	if f.err != nil {
		return nil, f.err
	}
	if f.page != nil {
		return f.page, nil
	}
	return &mailkube.ScheduledEmailPage{}, nil
}

func (f *fakeSchedules) All(
	_ context.Context, params mailkube.ScheduledEmailListParams,
) iter.Seq2[*mailkube.ScheduledEmail, error] {
	f.calls["all"]++
	f.listed = params

	return func(yield func(*mailkube.ScheduledEmail, error) bool) {
		if f.err != nil {
			yield(nil, f.err)
			return
		}
		for i := range f.items {
			if !yield(&f.items[i], nil) {
				return
			}
		}
	}
}

func (f *fakeSchedules) Get(_ context.Context, id string) (*mailkube.ScheduledEmail, error) {
	f.calls["get"]++
	if f.err != nil {
		return nil, f.err
	}
	if f.email != nil {
		return f.email, nil
	}
	return &mailkube.ScheduledEmail{ID: id, Status: "scheduled"}, nil
}

func (f *fakeSchedules) Update(
	_ context.Context, id string, params mailkube.ScheduledEmailUpdateParams,
) (*mailkube.ScheduledEmail, error) {
	f.calls["update"]++
	f.updated = params

	if f.err != nil {
		return nil, f.err
	}
	return &mailkube.ScheduledEmail{
		ID: id, Status: "scheduled", ScheduledAt: params.ScheduledAt.UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (f *fakeSchedules) Cancel(_ context.Context, id string) (*mailkube.CanceledScheduledEmail, error) {
	f.calls["cancel"]++
	if f.err != nil {
		return nil, f.err
	}
	return &mailkube.CanceledScheduledEmail{ID: id, Status: "canceled"}, nil
}

// fakeBatches stands in for the batch service.
type fakeBatches struct {
	canceled  int
	rescheded int
	calls     map[string]int
	err       error
}

func newFakeBatches() *fakeBatches { return &fakeBatches{calls: map[string]int{}} }

func (f *fakeBatches) Update(
	_ context.Context, batchID string, params mailkube.ScheduledEmailBatchUpdateParams,
) (*mailkube.ScheduledEmailBatchUpdate, error) {
	f.calls["update"]++
	if f.err != nil {
		return nil, f.err
	}
	return &mailkube.ScheduledEmailBatchUpdate{
		BatchID:          batchID,
		RescheduledCount: f.rescheded,
		ScheduledAt:      params.ScheduledAt.UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}

func (f *fakeBatches) Cancel(
	_ context.Context, batchID string,
) (*mailkube.ScheduledEmailBatchCancel, error) {
	f.calls["cancel"]++
	if f.err != nil {
		return nil, f.err
	}
	return &mailkube.ScheduledEmailBatchCancel{BatchID: batchID, CanceledCount: f.canceled}, nil
}

// interactive is the terminal model for a run that is allowed to ask questions.
//
// The default model in tests is a non-terminal, where a prompt must refuse rather than read: a
// confirmation answered from a pipe is a confirmation nobody gave. A test about what happens when
// someone answers has to say that someone is there.
func interactive() *output.Caps {
	return &output.Caps{
		TTY: true, Unicode: true, Interactive: true,
		Width: output.DefaultWidth, Glyphs: output.UnicodeGlyphs(),
	}
}

// result is one run of a scheduled-emails command, captured whole.
type result struct {
	out    string
	errOut string
	code   errs.Code
}

// runCmd executes one verb against the fakes and buffered streams.
func runCmd(
	t *testing.T, schedules *fakeSchedules, batches *fakeBatches, opts testsupport.TestOptions, args ...string,
) result {
	t.Helper()

	if opts.Env == nil {
		opts.Env = map[string]string{settings.EnvAPIKey: "mk_test"}
	}
	if opts.Globals == nil {
		opts.Globals = &settings.Globals{Output: "text"}
	}

	deps, out, errOut := testsupport.TestDeps(t, opts)

	f := scheduled.New()
	if schedules != nil {
		f.Schedules = func(*feature.Deps, settings.Resolved) (scheduled.ScheduleService, error) {
			return schedules, nil
		}
	}
	if batches != nil {
		f.Batches = func(*feature.Deps, settings.Resolved) (ports.BatchWriter, error) { return batches, nil }
	}

	cmd := f.Command(deps)
	cmd.SetArgs(testsupport.Args(args))
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	code := errs.CodeOK
	if err := cmd.Execute(); err != nil {
		detail := errs.Describe(err)
		code = detail.Code
		for _, line := range errs.Render(detail, deps.Caps.Glyphs.Cross) {
			errOut.WriteString(line + "\n")
		}
	}
	return result{out: out.String(), errOut: errOut.String(), code: code}
}

// samplePage is a page of three sends, two of them batched, for the listing tests.
func samplePage() *mailkube.ScheduledEmailPage {
	return &mailkube.ScheduledEmailPage{
		Pagination: mailkube.Pagination{
			TotalCount:  51,
			CurrentPage: 1,
			Steps:       mailkube.PageSteps{Next: "https://api.mailkube.com/mta/v1/scheduled-emails?page=2"},
		},
		Data: []mailkube.ScheduledEmail{
			{
				ID: "3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77", Status: "scheduled",
				ScheduledAt: "2026-08-14T09:32:00Z", Subject: "Reminder",
				Recipients: "alice@example.com",
			},
			{
				ID: "7b2e4d81-3c05-4e19-a7f2-16d90b4c8e31", Status: "scheduled",
				ScheduledAt: "2026-08-15T08:00:00Z", Subject: "Weekly digest",
				Recipients: "bob@example.com +2", BatchID: "digest-w33",
			},
			{
				ID: "9c04a1e7-88ba-4d02-9f61-2c7ea5310db4", Status: "scheduled",
				ScheduledAt: "2026-08-15T08:00:00Z", Subject: "Weekly digest",
				Recipients: "dana@example.com", BatchID: "digest-w33",
			},
		},
	}
}
