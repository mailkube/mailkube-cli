// Package output turns view models into what a user or a program reads.
//
// Two rules shape everything here. A command never formats a string itself: it returns a view
// model, and this package renders it, which is what keeps the human and machine forms two
// renderings of one value rather than two descriptions of one event. And every string the CLI did
// not construct itself is sanitised before it reaches a terminal, because a bounce reason is
// whatever a remote mail server chose to send.
package output

import (
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// DefaultWidth is the assumed terminal width when the real one cannot be discovered.
const DefaultWidth = 80

// Lookup reads one environment variable, reporting whether it was set at all.
//
// It is a function rather than a direct os call so terminal detection is testable: every rule
// below depends on the environment, and a test that had to mutate the process's own would not be
// able to run in parallel with any other.
type Lookup func(key string) (string, bool)

// OSEnv returns a Lookup reading the process environment.
func OSEnv() Lookup { return os.LookupEnv }

// MapEnv returns a Lookup reading a fixed map, for tests and for a caller building a scenario.
func MapEnv(env map[string]string) Lookup {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

// Caps is what the terminal can do, resolved once at startup and passed down.
//
// Resolving it once matters: a command that re-detected colour support per line could disagree
// with itself mid-screen, and a golden test would have no single place to pin.
type Caps struct {
	// TTY reports whether the success stream is a terminal.
	//
	// This is what decides the output format, and it is deliberately separate from Color: a
	// terminal with NO_COLOR set is still a terminal, and should still get human output.
	TTY bool
	// Color reports whether ANSI colour may be written.
	Color bool
	// Unicode reports whether non-ASCII glyphs may be written.
	Unicode bool
	// Width is the usable column count.
	Width int
	// Interactive reports whether the CLI may prompt.
	Interactive bool
	// Glyphs is the badge set matching Unicode.
	Glyphs Glyphs
}

// Glyphs are the badges the CLI prints in front of a line.
//
// They travel in a struct rather than as constants because there are two sets and the choice is
// made once, at detection. A command asks for caps.Glyphs.OK and never learns which set it got.
type Glyphs struct {
	// OK marks a success line.
	OK string
	// Cross marks a failure line.
	Cross string
	// Warn marks a line that is neither success nor failure.
	Warn string
	// Dup marks a duplicate that was recognised and not acted on twice.
	Dup string
	// Handshake marks a webhook registration challenge.
	Handshake string
	// Ellipsis marks a value that was shortened to fit.
	Ellipsis string
	// Bullet separates parts of a reference line.
	Bullet string
}

// UnicodeGlyphs is the badge set for a terminal that can draw them.
func UnicodeGlyphs() Glyphs {
	return Glyphs{
		OK: "✓", Cross: "✗", Warn: "⚠", Dup: "↻", Handshake: "🤝",
		Ellipsis: "…", Bullet: "·",
	}
}

// ASCIIGlyphs is the fallback set.
//
// The badges are bracketed words rather than punctuation lookalikes: on a terminal that cannot
// draw a check mark, a bare "x" in the first column is indistinguishable from content, whereas
// "[x]" reads as a badge in any font.
func ASCIIGlyphs() Glyphs {
	return Glyphs{
		OK: "[ok]", Cross: "[x]", Warn: "[!]", Dup: "[dup]", Handshake: "[hs]",
		Ellipsis: "...", Bullet: "-",
	}
}

// Detect resolves the terminal's capabilities from the streams and the environment.
func Detect(in io.Reader, out io.Writer, env Lookup) Caps {
	outTTY := isTerminal(out)

	caps := Caps{
		TTY:         outTTY,
		Color:       detectColor(env, outTTY),
		Unicode:     detectUnicode(env),
		Width:       detectWidth(out, env),
		Interactive: detectInteractive(in, out, env),
	}
	caps.Glyphs = ASCIIGlyphs()
	if caps.Unicode {
		caps.Glyphs = UnicodeGlyphs()
	}
	return caps
}

// isTerminal reports whether a stream is backed by a terminal.
//
// The type assertion is how a stream that is really an *os.File is told apart from the buffers a
// test injects, without this package importing os.File into its own signatures.
func isTerminal(stream any) bool {
	f, ok := stream.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

// detectColor applies the two cross-tool conventions plus the terminal check.
//
// NO_COLOR and CLICOLOR_FORCE are honoured because a user who has set them has already told every
// tool on their machine what they want, and a CLI that needs its own flag for the same thing is
// one more thing to configure.
func detectColor(env Lookup, outTTY bool) bool {
	if v, ok := env("NO_COLOR"); ok && v != "" {
		return false
	}
	if v, ok := env("CLICOLOR_FORCE"); ok && v != "" && v != "0" {
		return true
	}
	if v, _ := env("TERM"); v == "dumb" {
		return false
	}
	return outTTY
}

// detectUnicode decides whether non-ASCII glyphs are safe to print.
//
// The default is yes, because a modern terminal is UTF-8 and treating that as the exception would
// give most users the fallback set. Two things override it: MAILKUBE_ASCII, which is the explicit
// answer, and a POSIX locale that names an encoding other than UTF-8, which is the environment
// stating outright that it is not.
func detectUnicode(env Lookup) bool {
	if v, ok := env("MAILKUBE_ASCII"); ok && v != "" && v != "0" {
		return false
	}
	if locale, ok := firstSet(env, "LC_ALL", "LC_CTYPE", "LANG"); ok {
		upper := strings.ToUpper(locale)
		return strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
	}
	// A Windows console is the one place where no locale variable is the norm rather than a
	// signal, and where the legacy code page genuinely cannot draw these glyphs. Windows
	// Terminal sets WT_SESSION, and anything POSIX-flavoured enough to set TERM can be trusted.
	if runtime.GOOS == "windows" {
		_, modern := firstSet(env, "WT_SESSION", "TERM")
		return modern
	}
	return true
}

// detectWidth finds the usable column count, preferring what the terminal itself reports.
func detectWidth(out io.Writer, env Lookup) int {
	if f, ok := out.(interface{ Fd() uintptr }); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
	}
	// COLUMNS is what a pipeline or a CI runner uses to state a width it has no terminal to
	// report, so it is worth reading even though it is not exported by every shell.
	if v, ok := env("COLUMNS"); ok {
		if w, err := strconv.Atoi(v); err == nil && w > 0 {
			return w
		}
	}
	return DefaultWidth
}

// detectInteractive decides whether the CLI may ever stop and ask a question.
//
// Both streams must be terminals. Defining this on stdin alone is the classic mistake: a CI job
// that allocates a pty passes that test and then hangs forever on a prompt nobody can see, and it
// hangs at the point where the job has already done real work.
func detectInteractive(in io.Reader, out io.Writer, env Lookup) bool {
	return isTerminal(in) && isTerminal(out) && promptingAllowed(env)
}

// promptingAllowed applies the environment half of the interactivity rule.
//
// It is separate from the stream check so it can be tested on its own. That is not a cosmetic
// split: an automation environment always fails the stream check first, so a bug in these rules
// would never show up in a test that went through the whole detection, and these are the rules
// that stop a job hanging on a question nobody can answer.
func promptingAllowed(env Lookup) bool {
	if v, _ := env("TERM"); v == "dumb" {
		return false
	}
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "MAILKUBE_NO_PROMPT"} {
		if v, ok := env(key); ok && v != "" {
			return false
		}
	}
	return true
}

// firstSet returns the value of the first of the named variables that is set and non-empty.
func firstSet(env Lookup, keys ...string) (string, bool) {
	for _, key := range keys {
		if v, ok := env(key); ok && v != "" {
			return v, true
		}
	}
	return "", false
}
