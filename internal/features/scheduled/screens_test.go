package scheduled_test

import (
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/golden"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// TestScreens pins the screens this feature renders.
//
// The listing is the reason these live here rather than with the other screens: it needs data, and
// data means a substituted client, which the composition root deliberately offers no way to
// supply. The specification described these tables in prose; these files are what the program
// actually prints, and the prose is reconciled against them.
func TestScreens(t *testing.T) {
	t.Parallel()

	detailed := &mailkube.ScheduledEmail{
		ID:          "3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77",
		MessageID:   "<3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77@msg.mailkube.com>",
		Status:      "scheduled",
		ScheduledAt: "2026-08-14T09:32:00Z",
		CreatedAt:   "2026-08-14T07:02:00Z",
		Subject:     "Reminder",
		Recipients:  "alice@example.com",
		Topic:       "reminders",
		Tags:        []mailkube.Tag{{Name: "campaign", Value: "launch"}, {Name: "beta"}},
	}

	tests := []struct {
		name     string
		args     []string
		page     *mailkube.ScheduledEmailPage
		email    *mailkube.ScheduledEmail
		items    []mailkube.ScheduledEmail
		wantCode errs.Code
	}{
		{name: "list", args: []string{"list"}, page: samplePage()},
		{name: "list_empty", args: []string{"list"}},
		{
			name:  "list_all",
			args:  []string{"list", "--all"},
			items: samplePage().Data,
		},
		{name: "get", args: []string{"get", detailed.ID}, email: detailed},
		{
			name:     "error_status_sent",
			args:     []string{"list", "--status", "sent"},
			wantCode: errs.CodeUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schedules := newFakeSchedules()
			schedules.page = tc.page
			schedules.email = tc.email
			schedules.items = tc.items

			got := runCmd(t, schedules, nil, testsupport.TestOptions{}, tc.args...)
			if got.code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\n%s", got.code, tc.wantCode, got.errOut)
			}

			golden.Assert(t, tc.name+".out", []byte(got.out))
			golden.Assert(t, tc.name+".err", []byte(got.errOut))

			// The blanket rule every screen obeys: a failing command writes nothing to the
			// payload stream, so a caller piping it into a parser never sees half a document.
			if got.code != errs.CodeOK && got.out != "" {
				t.Errorf("a failing command wrote to stdout:\n%s", got.out)
			}
		})
	}
}
