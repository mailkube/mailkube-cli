package input

import (
	"strings"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// absoluteLayouts are the accepted spellings of an instant, tried in order.
//
// Every one of them carries a zone. That is the whole design of this parser, and the reason the
// list is short: a scheduling time is a promise about a moment, and a spelling that does not say
// which moment cannot be honoured.
func absoluteLayouts() []string {
	return []string{
		time.RFC3339,                // 2026-09-01T09:00:00+02:00
		"2006-01-02T15:04-07:00",    // the same, without seconds
		"2006-01-02 15:04:05-07:00", // a space instead of T, which is what people type
		"2006-01-02 15:04:05 -07:00",
		"2006-01-02 15:04-07:00",
		"2006-01-02 15:04 -07:00",
	}
}

// ParseAt resolves a scheduling time against the current instant.
//
// Three spellings are accepted: a relative offset (+2h), RFC 3339, and a wall time with an
// explicit offset. All three are unambiguous.
//
// A naive local wall time is deliberately refused. It is the one input whose meaning changes
// depending on where and when it is read, and around a daylight-saving boundary it can name an
// instant that occurs twice or not at all. Guessing costs the user a message sent an hour off, at
// a time they chose precisely because it mattered.
func ParseAt(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errs.Usagef("no time given")
	}

	if strings.HasPrefix(value, "+") {
		return parseRelative(value, now)
	}

	for _, layout := range absoluteLayouts() {
		if at, err := time.Parse(layout, value); err == nil {
			return at, nil
		}
	}
	return time.Time{}, unparseable(value)
}

// parseRelative handles the +2h form, which is relative to the clock the caller passed in.
func parseRelative(value string, now time.Time) (time.Time, error) {
	d, err := time.ParseDuration(value)
	if err != nil {
		return time.Time{}, errs.Validationf(
			"cannot read %q as a duration: use a form like +2h, +30m or +1h30m", value)
	}
	if d <= 0 {
		return time.Time{}, errs.Validationf("%q is not in the future", value)
	}
	return now.Add(d), nil
}

// unparseable explains the refusal, naming the missing offset when that is what went wrong.
//
// A user who typed a naive local time made a reasonable-looking mistake, and telling them only
// that the value is invalid leaves them to guess which part. Naming the offset, and showing their
// own value with one attached, is the difference between a message and a fix.
func unparseable(value string) error {
	if looksNaive(value) {
		return errs.Usagef(
			"%q has no UTC offset, so it names a different instant in every time zone.\n"+
				"Add one, for example %q, or use Z for UTC, or a relative +2h.",
			value, value+" +02:00")
	}
	return errs.Validationf(
		"cannot read %q as a time: use RFC 3339 (2026-09-01T09:00:00Z), a wall time with an "+
			"offset (\"2026-09-01 09:00 +02:00\"), or a relative offset (+2h)", value)
}

// looksNaive reports whether a value parses once an offset is appended, which is what tells a
// missing zone apart from a value that is simply not a time at all.
func looksNaive(value string) bool {
	for _, layout := range absoluteLayouts() {
		if _, err := time.Parse(layout, value+" +00:00"); err == nil {
			return true
		}
		if _, err := time.Parse(layout, value+"+00:00"); err == nil {
			return true
		}
	}
	return false
}
