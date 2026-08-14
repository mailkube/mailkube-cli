package errs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// indent is the two spaces every line under the headline carries.
const indent = "  "

// Render turns a Detail into the lines of a human error report, headline first.
//
// It returns lines rather than writing them so the caller owns the stream, and takes the cross
// glyph as an argument so this package never needs to know whether the terminal can draw one.
//
// The shape is fixed, and each part earns its place: what went wrong, what to do about it, and
// something to quote when asking for help. A report that only states the failure makes the reader
// guess at all three.
func Render(d Detail, cross string) []string {
	// A message may be several lines: server text passes through unaltered and is free to
	// contain newlines. Only the first shares the glyph's line; the rest align under it, so a
	// multi-line message reads as one report rather than as output that lost its indentation.
	head, rest := splitFirstLine(headline(d))
	lines := []string{cross + " " + head}
	for _, line := range rest {
		lines = append(lines, indent+line)
	}

	for _, hint := range d.Hints {
		lines = append(lines, indent+hint)
	}
	if d.RetryAfter > 0 {
		lines = append(lines, indent+"Retry after "+FormatRetryAfter(d.RetryAfter)+".")
	}
	if ref := reference(d); ref != "" {
		lines = append(lines, indent+ref)
	}
	if note := retryNote(d); note != "" {
		lines = append(lines, indent+note)
	}
	return lines
}

// splitFirstLine separates a possibly multi-line string into its first line and the rest.
func splitFirstLine(s string) (string, []string) {
	all := strings.Split(s, "\n")
	return all[0], all[1:]
}

// headline is the first line: the error name and the message, or just the message when the failure
// never reached the server and so has no name.
func headline(d Detail) string {
	msg := strings.TrimSpace(d.Message)
	if d.Name == "" {
		return msg
	}
	if msg == "" || msg == d.Name {
		return d.Name
	}
	return d.Name + " — " + msg
}

// reference is the line a user quotes to support.
//
// The request id is printed in full. It is a value someone copies into a support conversation, and
// an id shortened to fit a terminal is one that cannot be looked up.
func reference(d Detail) string {
	var parts []string
	if d.RequestID != "" {
		parts = append(parts, "request "+d.RequestID)
	}
	if d.StatusCode != 0 {
		parts = append(parts, "HTTP "+strconv.Itoa(d.StatusCode))
	}
	return strings.Join(parts, "  ·  ")
}

// retryNote states the retry position explicitly, in both directions.
//
// Saying nothing when a retry is worth attempting would be the same silence as when it is futile,
// and the difference is the whole reason a caller reads this line.
//
// The default is deliberately generic. Whether re-running is *safe* depends on what the command
// does — a second send is a second charged message unless it carries an idempotency key — and the
// kernel cannot know that. A command with something specific to say sets RetryNote.
func retryNote(d Detail) string {
	if d.RetryNote != "" {
		return d.RetryNote
	}
	switch {
	case d.Code == CodeAuth:
		return "Credentials are never retried automatically. Check them, then re-run."
	case d.Retryable:
		return "Nothing is retried automatically. Re-run the command yourself to try again."
	default:
		return ""
	}
}

// FormatRetryAfter renders a Retry-After value in seconds as a duration a human reads at a glance.
//
// The value is reported, never acted on: nothing in this CLI sleeps and retries by itself, because
// a tool that decides its own retry policy takes that decision away from the script running it.
func FormatRetryAfter(seconds int) string {
	if seconds <= 0 {
		return "0s"
	}
	d := time.Duration(seconds) * time.Second
	if d < time.Minute {
		return strconv.Itoa(seconds) + "s"
	}

	mins := int(d / time.Minute)
	rem := seconds - mins*60
	if rem == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm%ds", mins, rem)
}
