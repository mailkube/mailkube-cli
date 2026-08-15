package output_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// FuzzSanitize asserts the properties the sanitiser exists for, against arbitrary input.
//
// A table test can only assert the sequences someone thought of, and the interesting ones are the
// sequences nobody thought of: a truncated OSC, an ESC as the final byte, a CSI whose parameters
// run past the end of the string. Those are exactly the shapes a real bounce reason arrives in
// when a remote server truncates its own output.
//
// The seed corpus runs as an ordinary test on every build, so these cases are permanent
// regressions rather than something only the nightly job remembers.
func FuzzSanitize(f *testing.F) {
	// Every hostile character is written as an escape rather than as itself. A corpus holding a
	// real right-to-left override reverses the reading order of this file in an editor, which is
	// the attack being defended against, demonstrated on the person maintaining the defence.
	seeds := []string{
		"",
		"plain text",
		"\x1b]8;;https://evil.example\x07click me\x1b]8;;\x07", // OSC 8 hyperlink
		"\x1b]52;c;aGk=\x07",                 // OSC 52 clipboard write
		"\x1b[2J\x1b[H overwritten",          // clear screen, home cursor
		"\x1b[31mred\x1b[0m",                 // colour
		"\x1b",                               // a bare ESC at the very end
		"\x1b[",                              // a CSI that never terminates
		"\x1b]8;;",                           // an OSC that never terminates
		"before\nafter",                      // a forged row
		"before\r\nX-Injected: yes",          // a forged header
		"\u202egnp.exe",                      // right-to-left override
		"zero\u200bwidth\u200djoiner",        // zero-width characters
		"\x00\x01\x02\x7f",                   // C0 and DEL
		"\u0085\u009b31m\u009d",              // C1, including the 8-bit CSI
		"héllo wörld",                        // ordinary non-ASCII, must survive
		"日本語",                                // ordinary CJK, must survive
		string([]byte{0xff, 0xfe, 'o', 'k'}), // invalid UTF-8
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, in string) {
		got := output.Sanitize(in)

		if !utf8.ValidString(got) {
			t.Fatalf("Sanitize(%q) produced invalid UTF-8: %q", in, got)
		}
		// One line, always. A value that can introduce its own newline can forge a table row,
		// and a row is what a reader trusts.
		if strings.ContainsAny(got, "\r\n") {
			t.Fatalf("Sanitize(%q) kept a line break: %q", in, got)
		}
		// No escape introducer survives in any form. If one did, everything after it is a
		// sequence the terminal will act on.
		for _, r := range got {
			if r == 0x1B || r == 0x9B || r < 0x20 || (r >= 0x7F && r <= 0x9F) {
				t.Fatalf("Sanitize(%q) kept control %U: %q", in, r, got)
			}
		}
		// Idempotent, because sanitised text is sometimes rendered through a second layer, and
		// a second pass that changed the string would mean the first one left something behind.
		if again := output.Sanitize(got); again != got {
			t.Fatalf("Sanitize is not idempotent: %q then %q", got, again)
		}
	})
}

// FuzzClamp asserts that truncation never splits a rune and never exceeds its budget.
//
// The budget is display width, so the failure this catches is a CJK or emoji subject clamped by
// byte count: the row is either misaligned or the last character is half-written, and both look
// like a rendering bug in the terminal rather than in us.
func FuzzClamp(f *testing.F) {
	for _, s := range []string{
		"",
		"short",
		strings.Repeat("a", 200),
		"日本語のメールです",
		"\U0001f642\U0001f642\U0001f642\U0001f642",
	} {
		f.Add(s, 10)
	}

	f.Fuzz(func(t *testing.T, in string, max int) {
		if max < 0 || max > 1000 {
			t.Skip("a width outside anything a terminal reports")
		}

		got := output.Clamp(in, max, "…")

		// Clamp is not the sanitiser and does not repair its input: invalid bytes are Sanitize's
		// job, and every externally-sourced string passes through that first. What Clamp must
		// never do is *introduce* invalid UTF-8 by cutting a rune in half.
		if utf8.ValidString(in) && !utf8.ValidString(got) {
			t.Fatalf("Clamp(%q, %d) split a rune: %q", in, max, got)
		}
		// The budget is the whole point: a row whose cell overflows is a misaligned table, and
		// the ellipsis has to be paid for out of the same allowance.
		if width := output.DisplayWidth(got); width > max {
			t.Fatalf("Clamp(%q, %d) is %d columns wide: %q", in, max, width, got)
		}
		if got == in {
			return
		}
		if len([]rune(got)) > len([]rune(in)) {
			t.Fatalf("Clamp(%q, %d) grew the string: %q", in, max, got)
		}
	})
}
