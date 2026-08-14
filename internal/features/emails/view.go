package emails

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// SentView is what a send reports back.
//
// One view model covers three outcomes — sent, scheduled, replayed — because they are three
// answers to one question, and the fields they share are the same fields. A separate type per
// outcome would mean a script has to discover which shape it received before it can read an id.
type SentView struct {
	// ID is the accepted message's id, printed in full because a user copies it.
	ID string `json:"id"`
	// MessageID is the RFC Message-ID, when the platform returned one.
	MessageID string `json:"messageId,omitempty"`
	// To are the recipients the message was accepted for.
	To []string `json:"to"`
	// Replayed reports that this response replays an earlier identical request rather than
	// describing a second send. It is the one question --idempotency-key exists to answer.
	Replayed bool `json:"replayed"`
	// Scheduled reports that the send was accepted for later delivery.
	Scheduled bool `json:"scheduled"`
	// Status is the scheduled send's status, on a scheduled acknowledgement only.
	Status string `json:"status,omitempty"`
	// ScheduledAt is when it is due, verbatim as the server sent it.
	ScheduledAt string `json:"scheduledAt,omitempty"`
	// BatchID is the batch it was grouped under, if any.
	BatchID string `json:"batchId,omitempty"`

	// due is the human rendering of ScheduledAt, resolved against the injected clock at
	// construction. It cannot be computed in RenderText, which has no clock and must not have
	// one: a view model that read the wall clock would render differently on every run.
	due string
}

// RenderText implements output.TextRenderer.
func (v SentView) RenderText(caps output.Caps) []string {
	lines := []string{v.headline(caps)}
	for _, row := range v.table().Lines() {
		lines = append(lines, "  "+row)
	}
	if !v.Scheduled {
		return lines
	}
	// A scheduled send is the one outcome with something left to do, and the id in the
	// suggested command is the full one: an abbreviated id in a command a user is told to run
	// is a command that fails.
	return append(lines,
		"",
		"  Cancel it with:",
		"    mailkube scheduled-emails cancel "+v.ID,
	)
}

// headline is the first line, which states which of the three outcomes this is.
func (v SentView) headline(caps output.Caps) string {
	switch {
	case v.Replayed:
		return caps.Glyphs.Dup + " Replayed (not re-sent)"
	case v.Scheduled:
		return caps.Glyphs.OK + " Scheduled"
	default:
		return caps.Glyphs.OK + " Sent"
	}
}

// table is the label/value block under the headline.
func (v SentView) table() output.Table {
	table := output.Table{Rows: [][]string{{"id", v.ID}}}
	if v.MessageID != "" {
		table.Rows = append(table.Rows, []string{"message-id", v.MessageID})
	}
	if !v.Scheduled {
		table.Rows = append(table.Rows, []string{"to", strings.Join(v.To, ", ")})
		return table
	}

	table.Rows = append(table.Rows, []string{"status", v.Status})
	table.Rows = append(table.Rows, []string{"scheduled-at", v.due})
	if v.BatchID != "" {
		table.Rows = append(table.Rows, []string{"batch", v.BatchID})
	}
	return table
}

// DryRunView is the request a send would have made.
//
// It carries the method and the URL as well as the body because the two questions a dry run is
// asked are "what am I sending" and "where am I sending it": a base URL whose version segment was
// lost is invisible in the body alone.
type DryRunView struct {
	// Method is the HTTP method the SDK would use.
	Method string `json:"method"`
	// URL is the resolved endpoint, after base-URL normalisation.
	URL string `json:"url"`
	// Body is the request body, with attachment content elided.
	Body json.RawMessage `json:"body"`
	// DryRun is always true, so a machine reading this can tell it apart from a send.
	DryRun bool `json:"dryRun"`
}

// RenderText implements output.TextRenderer.
func (v DryRunView) RenderText(_ output.Caps) []string {
	lines := []string{v.Method + " " + v.URL}
	lines = append(lines, strings.Split(strings.TrimRight(string(v.Body), "\n"), "\n")...)
	return append(lines, "(dry run — nothing was sent)")
}

// humanSize renders a byte count the way a person reads one.
func humanSize(bytes int) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	value := float64(bytes)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TiB", value/unit)
}

// humanTime renders an instant the way every screen in this CLI renders one.
//
// A value the server sent that this release cannot parse is printed as it arrived rather than
// dropped or guessed at. The server owns its timestamps, and a display that silently omits one it
// did not recognise is worse than one that shows an unfamiliar string.
func humanTime(value string) string {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return at.UTC().Format("2006-01-02 15:04") + " UTC"
}

// relative renders the distance between two instants in the coarsest unit that stays informative.
//
// Every unit rounds rather than truncating, and the thresholds sit at one and a half units rather
// than at one. Both are there for the same reason: a send scheduled for +3h takes a moment to
// reach the server, so the acknowledgement describes something a few milliseconds under three
// hours away. Truncating that to "in 2 hours" tells the user their own flag was not honoured.
func relative(from, to time.Time) string {
	d := to.Sub(from)
	if d <= 0 {
		return "now"
	}

	switch {
	case d < 90*time.Second:
		return fmt.Sprintf("in %d seconds", rounded(d, time.Second))
	case d < 90*time.Minute:
		return plural(rounded(d, time.Minute), "minute")
	case d < 36*time.Hour:
		return plural(rounded(d, time.Hour), "hour")
	default:
		return plural(rounded(d, 24*time.Hour), "day")
	}
}

// rounded returns the duration in whole units, rounded to the nearest.
func rounded(d, unit time.Duration) int { return int((d + unit/2) / unit) }

// plural renders "in 1 hour" and "in 2 hours" from one call site.
func plural(n int, unit string) string {
	if n == 1 {
		return "in 1 " + unit
	}
	return fmt.Sprintf("in %d %ss", n, unit)
}
