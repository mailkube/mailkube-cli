package doctor

import (
	"strconv"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// CheckView is one line of the report.
type CheckView struct {
	// Label is what was checked.
	Label string `json:"label"`
	// Status is the verdict, as a word rather than a number, because this document is read by
	// people as often as by programs and "2" is not a verdict anyone recognises.
	Status string `json:"status"`
	// Detail is the one-line explanation.
	Detail string `json:"detail"`
}

// ReportView is the whole diagnostic.
type ReportView struct {
	// Checks are the findings, in the order they were run.
	Checks []CheckView `json:"checks"`
	// Warnings is how many checks warned.
	Warnings int `json:"warnings"`
	// Failures is how many checks failed.
	Failures int `json:"failures"`
}

// add records one finding and keeps the counts in step with it.
//
// The counts are maintained here rather than recomputed at render time so that the summary line
// and the rows can never disagree, which is the one thing a reader of this screen would not
// think to check.
func (v *ReportView) add(label string, f feature.Finding) {
	v.Checks = append(v.Checks, CheckView{Label: label, Status: statusWord(f.Status), Detail: f.Detail})
	switch f.Status {
	case feature.StatusWarn:
		v.Warnings++
	case feature.StatusFail:
		v.Failures++
	case feature.StatusOK:
	}
}

// Verdict is the report's own conclusion, as the error that carries the exit code.
//
// One code for any number of failed checks, rather than a code derived from whichever check
// failed. A report can fail in several ways at once, so deriving would mean picking one, and the
// rule for picking would end up being the order the checks happen to run in. Exit 5 says the
// environment is not in a usable state; the rows say which part of it.
//
// The message names the count and points at the report rather than repeating it. Whoever is
// reading this has the whole thing on screen already.
func (v ReportView) Verdict(strict bool) error {
	switch {
	case v.Failures > 0:
		return errs.Newf(errs.CodeConfig,
			"%s above: this environment is not ready.", plural(v.Failures, "check failed", "checks failed"))
	case strict && v.Warnings > 0:
		return errs.Newf(errs.CodeConfig,
			"%s above, and --strict counts them as failures.", plural(v.Warnings, "warning", "warnings"))
	default:
		return nil
	}
}

// statusWord renders a verdict.
func statusWord(s feature.Status) string {
	switch s {
	case feature.StatusOK:
		return "ok"
	case feature.StatusWarn:
		return "warn"
	case feature.StatusFail:
		return "fail"
	default:
		return "unknown"
	}
}

// RenderText implements output.TextRenderer.
func (v ReportView) RenderText(caps output.Caps) []string {
	table := output.Table{}
	for _, c := range v.Checks {
		table.Rows = append(table.Rows, []string{badge(caps, c.Status), c.Label, c.Detail})
	}

	lines := make([]string, 0, len(v.Checks)+2)
	for _, line := range table.Lines() {
		lines = append(lines, "  "+line)
	}
	return append(lines, "", "  "+summary(v))
}

// badge renders a verdict as the glyph for it.
func badge(caps output.Caps, status string) string {
	switch status {
	case "warn":
		return caps.Glyphs.Warn
	case "fail":
		return caps.Glyphs.Cross
	default:
		return caps.Glyphs.OK
	}
}

// summary is the closing line, which is what a reader looks at first.
func summary(v ReportView) string {
	switch {
	case v.Failures > 0:
		return plural(v.Failures, "failure", "failures") + ", " + plural(v.Warnings, "warning", "warnings") + "."
	case v.Warnings > 0:
		return plural(v.Warnings, "warning", "warnings") + "."
	default:
		return "Everything checks out."
	}
}

// plural renders a count with its wording, so the summary reads as a sentence.
//
// Both forms are given rather than an "s" appended, because not every phrase here is a noun that
// pluralises that way: "1 check failed" becomes "2 checks failed", not "2 check faileds".
func plural(n int, singular, many string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + many
}
