package input_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/input"
)

// FuzzHeaderFlag asserts that parsing one --header occurrence is total and lossless.
//
// Total, because a flag parser that panics turns a typo into a stack trace. Lossless, because the
// value is what ends up on the wire: the split takes the first separator only, so a header value
// containing a colon must survive intact rather than being silently cut short.
func FuzzHeaderFlag(f *testing.F) {
	for _, s := range []string{
		"",
		"X-Campaign: launch",
		"X-Campaign:launch",
		"X-Link: https://example.com:8443/path",  // a value with more separators than the split
		"X-Campaign: ",                           // an empty value, which is legal
		": no name",                              // an empty name, which is not
		"no separator at all",                    //
		"X-A: b\r\nX-Injected: yes",              // injection, rejected later by validation
		"  X-Padded  :   value   ",               // trimming on both halves
		"X-Unicode: héllo",                       //
		strings.Repeat("X", 5000) + ": overlong", //
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		flag := input.NewHeaderFlag()

		if err := flag.Set(raw); err != nil {
			// A refusal is a legitimate outcome. What it may never be is silent: the message
			// has to name the flag's own noun, or the user cannot tell which flag refused.
			if !strings.Contains(err.Error(), "header") {
				t.Fatalf("Set(%q) refused without naming what it was parsing: %v", raw, err)
			}
			return
		}

		pairs := flag.Pairs()
		if len(pairs) != 1 {
			t.Fatalf("Set(%q) collected %d pairs, want 1", raw, len(pairs))
		}
		if pairs[0].Key == "" {
			t.Fatalf("Set(%q) accepted an empty name", raw)
		}
		// Only the first separator splits. Reassembling the trimmed halves has to give back
		// the trimmed input, or something in the middle was dropped.
		rejoined := pairs[0].Key + ":" + pairs[0].Value
		if strings.TrimSpace(collapse(raw)) != strings.TrimSpace(collapse(rejoined)) {
			t.Fatalf("Set(%q) did not round-trip: %q", raw, rejoined)
		}
	})
}

// collapse removes the padding the parser is allowed to trim, so the round-trip compares content
// rather than whitespace.
func collapse(s string) string {
	key, value, found := strings.Cut(s, ":")
	if !found {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(key) + ":" + strings.TrimSpace(value)
}

// FuzzParseAt asserts that the schedule parser never accepts what it cannot mean.
//
// The dangerous input is not the malformed one, which is refused, but the plausible one: a naive
// local wall time has no single meaning across a DST boundary, so it must be refused rather than
// guessed at. Every value this accepts has to come back with a zone.
func FuzzParseAt(f *testing.F) {
	for _, s := range []string{
		"2026-09-01T09:00:00Z",
		"2026-09-01T09:00:00+02:00",
		"2026-09-01 09:00 +02:00",
		"2026-09-01 09:00", // naive: must be refused
		"+2h", "+90m", "+0s", "-2h",
		"", "  ", "tomorrow", "+", "+h", "++2h",
		"999999999999h",
	} {
		f.Add(s)
	}

	// A fixed instant, so a relative input resolves to the same absolute time on every run.
	now := time.Date(2026, time.August, 14, 7, 32, 0, 0, time.UTC)

	f.Fuzz(func(t *testing.T, raw string) {
		got, err := input.ParseAt(raw, now)
		if err != nil {
			return
		}
		// Anything accepted must name a future instant. The zero time is what every other part
		// of this program reads as "unset", so a parser that can return it silently turns a
		// scheduled send into an immediate one.
		if !got.After(now) {
			t.Fatalf("ParseAt(%q) accepted a time that is not in the future: %s", raw, got)
		}
		// A parsed instant is absolute. If the zone were unset the value would mean something
		// different on the machine that sends it than on the one that scheduled it.
		if _, offset := got.Zone(); offset == 0 && got.Location() == nil {
			t.Fatalf("ParseAt(%q) returned a time with no location", raw)
		}
	})
}
