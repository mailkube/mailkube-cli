package output

import (
	"strings"
	"unicode"
)

// replacement stands in for a byte sequence that is not valid UTF-8.
const replacement = "�"

// Sanitize makes a string from outside the CLI safe to print to a terminal.
//
// It applies to every value the CLI did not construct itself: API and webhook payload fields, and
// SMTP server response text. A bounce reason is whatever a remote mail server chose to send, an
// email subject is whatever the sender typed, and both are rendered on a screen that interprets
// control sequences.
//
// The threat is not cosmetic. An ANSI OSC 8 sequence embeds a hyperlink whose visible text says
// one thing and whose target says another; OSC 52 writes to the user's clipboard; CSI sequences
// reposition the cursor and can overwrite lines already printed, including the ones stating what
// the CLI just did. Right-to-left overrides reverse the reading order of a domain name. All of
// these arrive as ordinary characters inside an ordinary string field.
//
// The invariant this serves is stated once and holds everywhere: raw bytes go to files and pipes,
// sanitised text goes to terminals. A --record file and a --forward request carry exactly what
// arrived, byte for byte, because the downstream consumer verifies a signature over it.
//
// The result is one line. Line breaks are removed along with the other C0 controls, because this
// renders a field inside a row or a table cell, and a value that can introduce its own newline can
// forge a row.
func Sanitize(s string) string {
	runes := []rune(strings.ToValidUTF8(s, replacement))

	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1B {
			i = skipEscape(runes, i)
			continue
		}
		if removable(runes[i]) {
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

// skipEscape returns the index of the last rune of the escape sequence starting at i.
//
// Dropping only the ESC byte would leave the parameters behind as visible junk, and worse, would
// leave a sequence that a terminal could still resynchronise on. The whole sequence goes.
func skipEscape(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return i
	}

	switch runes[i+1] {
	case '[':
		// CSI: parameter and intermediate bytes, then a final byte in @ to ~.
		return skipUntilFunc(runes, i+2, func(r rune) bool { return r >= 0x40 && r <= 0x7E })
	case ']', 'P', 'X', '^', '_':
		// A string sequence, terminated by BEL or by ST (ESC \). OSC is the dangerous one:
		// it is how a hyperlink and a clipboard write are spelled.
		return skipString(runes, i+2)
	default:
		// Everything else is a two-byte escape.
		return i + 1
	}
}

// skipUntilFunc returns the index of the first rune from i satisfying done, or the last index.
func skipUntilFunc(runes []rune, i int, done func(rune) bool) int {
	for ; i < len(runes); i++ {
		if done(runes[i]) {
			return i
		}
	}
	return len(runes) - 1
}

// skipString consumes a control-string body up to and including its terminator.
func skipString(runes []rune, i int) int {
	for ; i < len(runes); i++ {
		if runes[i] == 0x07 { // BEL
			return i
		}
		if runes[i] == 0x1B && i+1 < len(runes) && runes[i+1] == '\\' { // ST
			return i + 1
		}
	}
	return len(runes) - 1
}

// removable reports whether a rune is dropped outright.
//
// Three groups, each for its own reason. Control characters (C0, DEL, C1) drive the terminal
// rather than appearing on it. Bidirectional formatting characters reorder what follows them, so
// a value can be made to read as something other than what it is. Zero-width characters occupy no
// space, which makes them invisible padding inside a value that a user is comparing by eye.
func removable(r rune) bool {
	switch {
	case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F:
		return true
	case r >= 0x200B && r <= 0x200F, r == 0x2028, r == 0x2029:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0xFEFF:
		return true
	default:
		return false
	}
}

// Clamp shortens a string to a display width, marking that it was shortened.
//
// Width is measured in columns rather than bytes or runes, so a CJK subject or an emoji cannot
// overflow the space reserved for it. The ellipsis is passed in rather than assumed, because on an
// ASCII terminal it is three characters wide and clamping to a width that ignored that would put
// the column back over its budget.
func Clamp(s string, max int, ellipsis string) string {
	if max <= 0 {
		return ""
	}
	if DisplayWidth(s) <= max {
		return s
	}

	budget := max - DisplayWidth(ellipsis)
	if budget <= 0 {
		return ellipsis
	}

	var b strings.Builder
	used := 0
	for _, r := range s {
		w := runeWidth(r)
		if used+w > budget {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + ellipsis
}

// Field renders one externally-sourced value into a column: sanitised, then clamped.
//
// The order is not interchangeable. Clamping first could cut an escape sequence in half and leave
// the remainder to be printed as content, so sanitising has to see the whole value.
func Field(s string, max int, ellipsis string) string {
	return Clamp(Sanitize(s), max, ellipsis)
}

// DisplayWidth is the number of terminal columns a string occupies.
func DisplayWidth(s string) int {
	total := 0
	for _, r := range s {
		total += runeWidth(r)
	}
	return total
}

// runeWidth is the column count of a single rune.
//
// This is an approximation of the Unicode east-asian-width property, kept as an explicit range
// table rather than pulled in as a dependency: the ranges that matter for the values this CLI
// prints are CJK text and emoji, both of which are contiguous and stable.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r):
		// A combining mark attaches to the preceding character and adds no column of its own.
		return 0
	case wide(r):
		return 2
	default:
		return 1
	}
}

// wideRanges are the code-point ranges that occupy two columns.
//
// A table rather than a chain of conditions: this is data, and written as control flow it reads
// as a decision being made when it is really a lookup being performed.
func wideRanges() [][2]rune {
	return [][2]rune{
		{0x1100, 0x115F},   // Hangul Jamo
		{0x2E80, 0xA4CF},   // CJK radicals through Yi
		{0xAC00, 0xD7A3},   // Hangul syllables
		{0xF900, 0xFAFF},   // CJK compatibility ideographs
		{0xFE10, 0xFE19},   // vertical forms
		{0xFE30, 0xFE6F},   // CJK compatibility forms
		{0xFF00, 0xFF60},   // fullwidth forms
		{0xFFE0, 0xFFE6},   // fullwidth signs
		{0x1F300, 0x1FAFF}, // emoji and pictographs
		{0x20000, 0x3FFFD}, // CJK extension planes
	}
}

// wide reports whether a rune occupies two columns.
func wide(r rune) bool {
	if r == 0x303F {
		// The one narrow character inside the CJK block, so it is excluded by name rather
		// than by splitting the range around it.
		return false
	}
	for _, span := range wideRanges() {
		if r >= span[0] && r <= span[1] {
			return true
		}
	}
	return false
}
