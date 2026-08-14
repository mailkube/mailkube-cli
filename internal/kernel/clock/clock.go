// Package clock is the CLI's only source of the current time.
//
// Reading the wall clock directly is what makes output untestable: a golden file containing a
// timestamp is a golden file that fails a second later. Every command takes its time from here, and
// forbidigo denies time.Now everywhere else, so the substitution cannot be forgotten.
package clock

import "time"

// Clock reports the current time.
//
// One method, because that is all any caller needs. A wider interface would drag sleeping and
// timers in with it, and those belong to whoever is waiting, not to a shared primitive.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// System reads the machine's clock. This is what the binary runs with.
type System struct{}

// Now returns the current time from the operating system.
func (System) Now() time.Time {
	return time.Now() //nolint:forbidigo // the one permitted reading of the wall clock
}

// Fixed always reports the same instant. Tests use it so rendered output is reproducible.
type Fixed struct {
	// At is the instant every call returns.
	At time.Time
}

// Now returns the fixed instant.
func (f Fixed) Now() time.Time { return f.At }

// Testing is the instant golden files are rendered at.
//
// One shared value, so every golden in the repo agrees on what "now" was. Picking a per-test
// instant instead would make two goldens rendering the same screen disagree for no reason a
// reviewer could see in the diff.
func Testing() Fixed {
	return Fixed{At: time.Date(2026, time.August, 14, 7, 32, 0, 0, time.UTC)}
}
