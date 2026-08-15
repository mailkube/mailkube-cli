package emails

import (
	"testing"
	"time"
)

func TestHumanSizeReadsAsAPersonWouldSayIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		bytes int
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{5 << 20, "5.0 MiB"},
		{3 << 30, "3.0 GiB"},
		{2 << 40, "2.0 TiB"},
	}

	for _, tc := range tests {
		if got := humanSize(tc.bytes); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestRelativeRoundsRatherThanTruncating(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		after time.Duration
		want  string
	}{
		{"already due", -time.Second, "now"},
		{"seconds", 30 * time.Second, "in 30 seconds"},
		{"just over a minute", time.Minute + 20*time.Second, "in 80 seconds"},
		{"minutes", 45 * time.Minute, "in 45 minutes"},
		{"one minute", 100 * time.Second, "in 2 minutes"},
		// The case that matters: a send scheduled for +3h takes a moment to reach the
		// server, so the acknowledgement is a few milliseconds short of three hours away.
		// Truncating that to "in 2 hours" tells the user their own flag was ignored.
		{"just under three hours", 3*time.Hour - 40*time.Millisecond, "in 3 hours"},
		{"just over an hour", time.Hour + time.Minute, "in 61 minutes"},
		{"an hour and a half", 100 * time.Minute, "in 2 hours"},
		{"days", 50 * time.Hour, "in 2 days"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := relative(now, now.Add(tc.after)); got != tc.want {
				t.Errorf("relative(+%v) = %q, want %q", tc.after, got, tc.want)
			}
		})
	}
}

func TestATimeTheServerSentIsRenderedInUTC(t *testing.T) {
	t.Parallel()

	if got := humanTime("2026-08-14T11:32:00+02:00"); got != "2026-08-14 09:32 UTC" {
		t.Errorf("humanTime = %q, want the instant in UTC", got)
	}
}

func TestAnUnparseableServerTimeIsShownAsItArrived(t *testing.T) {
	t.Parallel()

	// The server owns its timestamps. A display that dropped one it did not recognise, or
	// guessed at it, would be worse than one showing an unfamiliar string.
	const odd = "sometime next week"
	if got := humanTime(odd); got != odd {
		t.Errorf("humanTime(%q) = %q, want it unchanged", odd, got)
	}
}
