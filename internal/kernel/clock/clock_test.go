package clock_test

import (
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
)

func TestSystemReportsTheCurrentTime(t *testing.T) {
	t.Parallel()

	before := time.Now()
	got := clock.System{}.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Errorf("Now() = %v, want between %v and %v", got, before, after)
	}
}

// The whole point of the seam: two readings a moment apart are identical, so a timestamp in
// rendered output does not change between the run that wrote a golden and the run that checks it.
func TestFixedNeverMoves(t *testing.T) {
	t.Parallel()

	c := clock.Fixed{At: time.Date(2026, time.August, 14, 9, 32, 0, 0, time.UTC)}

	first := c.Now()
	time.Sleep(time.Millisecond)
	if second := c.Now(); !second.Equal(first) {
		t.Errorf("Now() moved: %v then %v", first, second)
	}
	if !first.Equal(c.At) {
		t.Errorf("Now() = %v, want %v", first, c.At)
	}
}

func TestTestingClockIsUTC(t *testing.T) {
	t.Parallel()

	at := clock.Testing().Now()
	if at.Location() != time.UTC {
		t.Errorf("the golden clock is in %v, want UTC", at.Location())
	}
	if at.IsZero() {
		t.Error("the golden clock reads the zero time")
	}
}

// Both implementations have to satisfy the interface, or a test can substitute one and the binary
// cannot use the other.
func TestBothImplementationsSatisfyClock(t *testing.T) {
	t.Parallel()

	var _ clock.Clock = clock.System{}
	var _ clock.Clock = clock.Fixed{}
}
