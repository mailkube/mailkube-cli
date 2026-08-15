package errors

import (
	"strconv"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// labelWidth is the column the action text starts in, so Check and Fix lines align.
const labelWidth = 7

// RenderText implements output.TextRenderer.
//
// The shape is the same for both halves of the catalogue: what it is, then what happened, then
// what to do. A reader who has seen one explanation can read the next one without re-learning the
// layout.
func (e Entry) RenderText(caps output.Caps) []string {
	lines := []string{e.heading(caps), ""}
	lines = append(lines, indented(wrap(e.Summary, body(caps)))...)

	if len(e.Actions) > 0 {
		lines = append(lines, "")
	}
	for _, action := range e.Actions {
		lines = append(lines, labelled(action.Kind, action.Detail, caps)...)
	}
	if e.Note != "" {
		lines = append(lines, "")
		lines = append(lines, labelled("Note", e.Note, caps)...)
	}
	return lines
}

// heading is the first line: the identity, the transport's own code, and the retry position.
func (e Entry) heading(caps output.Caps) string {
	bullet := "  " + caps.Glyphs.Bullet + "  "

	// The enhanced status is part of the identity, not an extra: 454 4.7.0 and a bare 454 are
	// different situations, and it is the pair a bounce message quotes.
	name := e.Name
	if e.Enhanced != "" {
		name += " " + e.Enhanced
	}

	parts := []string{name}
	if e.SMTP {
		parts = append(parts, "SMTP reply")
	} else if e.Status != 0 {
		parts = append(parts, "HTTP "+strconv.Itoa(e.Status))
	}
	parts = append(parts, retryWord(e.Retryable))
	return strings.Join(parts, bullet)
}

// retryWord states the retry position in both directions.
//
// Saying nothing when re-running is worth trying would be the same silence as when it is futile,
// and telling the two apart is most of why someone reads this.
func retryWord(retryable bool) string {
	if retryable {
		return "retryable"
	}
	return "not retryable"
}

// labelled renders one Check, Fix or Note, with continuation lines under the text rather than
// under the label.
func labelled(label, detail string, caps output.Caps) []string {
	pad := strings.Repeat(" ", max(labelWidth-len(label), 1))
	wrapped := wrap(detail, body(caps)-labelWidth)

	lines := []string{"  " + label + pad + wrapped[0]}
	for _, continuation := range wrapped[1:] {
		lines = append(lines, "  "+strings.Repeat(" ", labelWidth)+continuation)
	}
	return lines
}

// ListView is every name and code this command explains.
type ListView struct {
	// API are the error names the platform's REST API returns.
	API []Entry `json:"api"`
	// SMTP are the submission reply codes.
	SMTP []Entry `json:"smtp"`
}

// listView builds the catalogue listing.
func listView() ListView {
	return ListView{API: apiCatalogue(), SMTP: smtpCatalogue()}
}

// RenderText implements output.TextRenderer.
func (v ListView) RenderText(_ output.Caps) []string {
	lines := []string{"API error names:"}
	lines = append(lines, summarise(v.API)...)
	lines = append(lines, "", "SMTP reply codes:")
	lines = append(lines, summarise(v.SMTP)...)

	return append(lines, "", "  Explain one with `mailkube errors explain <name>`.")
}

// summarise renders the name-and-sentence rows of a listing.
func summarise(entries []Entry) []string {
	table := output.Table{}
	for _, entry := range entries {
		name := entry.Name
		if entry.Enhanced != "" {
			name += " " + entry.Enhanced
		}
		table.Rows = append(table.Rows, []string{name, entry.Summary})
	}

	lines := table.Lines()
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return lines
}

// body is the width available for prose, allowing for the indent it sits in.
func body(caps output.Caps) int {
	width := caps.Width
	if width <= 0 {
		width = output.DefaultWidth
	}
	// A hard floor, because a very narrow terminal would otherwise produce a column of single
	// words, which is less readable than a line that overflows.
	return max(width-4, 40)
}

// indented shifts lines into the block under a heading.
func indented(lines []string) []string {
	shifted := make([]string, 0, len(lines))
	for _, line := range lines {
		shifted = append(shifted, "  "+line)
	}
	return shifted
}

// wrap breaks text on spaces to fit a width, always returning at least one line.
//
// It never breaks inside a word, so a URL stays selectable with a double-click even when it
// overflows — which is the one case where overflowing is better than fitting.
func wrap(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}

	var (
		lines   []string
		current = words[0]
	)
	for _, word := range words[1:] {
		if output.DisplayWidth(current)+1+output.DisplayWidth(word) > width {
			lines = append(lines, current)
			current = word
			continue
		}
		current += " " + word
	}
	return append(lines, current)
}
