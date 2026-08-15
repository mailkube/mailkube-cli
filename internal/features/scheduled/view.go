package scheduled

import (
	"strconv"
	"strings"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// shortIDLength is how much of an id a table shows.
//
// Tables and event lines abbreviate; detail blocks, machine formats and every command a user is
// told to run carry the id in full. A truncated id in a copy-pasteable command is a command that
// fails, which is worse than a wide column.
const shortIDLength = 8

// ItemView is one scheduled send as a listing row.
type ItemView struct {
	// ID is the full id. The abbreviation happens at render time, so a machine reading this
	// gets something it can pass back to `get`.
	ID string `json:"id"`
	// Status is scheduled, canceled or failed.
	Status string `json:"status"`
	// ScheduledAt is when it is due, verbatim as the server sent it.
	ScheduledAt string `json:"scheduledAt"`
	// Subject is the message subject.
	Subject string `json:"subject"`
	// Recipients is the server's summary string, not a list: the first recipient plus an
	// overflow count. The full list stays with the frozen payload.
	Recipients string `json:"recipients"`
	// BatchID is the batch label, if any.
	BatchID string `json:"batchId,omitempty"`
	// Topic is the mailing-list topic, if any.
	Topic string `json:"topic,omitempty"`

	// due is the human rendering of ScheduledAt, resolved at construction because a view
	// model must not read a clock of its own.
	due string
}

// ListView is a listing, of one page or of everything.
type ListView struct {
	// Items are the rows.
	Items []ItemView `json:"items"`
	// TotalCount is how many match across every page, when the server said.
	TotalCount int `json:"totalCount,omitempty"`
	// Page is the 1-based page in hand, when this is one page.
	Page int `json:"page,omitempty"`
	// Pages is how many pages there are, when it can be worked out.
	Pages int `json:"pages,omitempty"`
	// HasMore reports that the server offered a following page.
	HasMore bool `json:"hasMore"`
	// Complete reports that nothing was left out of this answer.
	Complete bool `json:"complete"`
}

// RenderText implements output.TextRenderer.
func (v ListView) RenderText(caps output.Caps) []string {
	if len(v.Items) == 0 {
		return []string{"No scheduled sends match."}
	}

	table := output.Table{Headers: []string{"ID", "SCHEDULED AT", "SUBJECT", "RECIPIENTS", "BATCH"}}
	for _, item := range v.Items {
		table.Rows = append(table.Rows, []string{
			short(item.ID),
			item.due,
			item.Subject,
			item.Recipients,
			orDash(item.BatchID, caps),
		})
	}

	return append(table.Lines(), "", v.summary(caps))
}

// summary is the line under the table saying where in the collection this is.
func (v ListView) summary(caps output.Caps) string {
	count := strconv.Itoa(len(v.Items))

	if v.Page == 0 {
		// A complete walk needs no page arithmetic; an interrupted one has already said so
		// on the error stream, and repeats it here so the payload stands on its own.
		if v.Complete {
			return count + " scheduled sends."
		}
		return count + " scheduled sends shown; more match. Raise --max-items."
	}

	line := count + " of " + strconv.Itoa(v.TotalCount) +
		" " + caps.Glyphs.Bullet + " page " + strconv.Itoa(v.Page)
	if v.Pages > 0 {
		line += " of " + strconv.Itoa(v.Pages)
	}
	if v.HasMore {
		line += "   (--all walks every page)"
	}
	return line
}

// DetailView is one scheduled send in full.
type DetailView struct {
	// ID is the full id.
	ID string `json:"id"`
	// MessageID is the RFC Message-ID the message will carry.
	MessageID string `json:"messageId,omitempty"`
	// Status is scheduled, canceled or failed.
	Status string `json:"status"`
	// ScheduledAt is when it is due, verbatim.
	ScheduledAt string `json:"scheduledAt"`
	// CreatedAt is when it was accepted, verbatim.
	CreatedAt string `json:"createdAt,omitempty"`
	// Subject is the message subject.
	Subject string `json:"subject"`
	// Recipients is the server's summary string.
	Recipients string `json:"recipients"`
	// BatchID is the batch label, if any.
	BatchID string `json:"batchId,omitempty"`
	// Topic is the mailing-list topic, if any.
	Topic string `json:"topic,omitempty"`
	// Tags are the message tags attached at send time.
	Tags []mailkube.Tag `json:"tags,omitempty"`

	// due is ScheduledAt rendered for a person, including how far away it is.
	due string
}

// RenderText implements output.TextRenderer.
func (v DetailView) RenderText(caps output.Caps) []string {
	table := output.Table{Rows: [][]string{
		{"id", v.ID},
		{"status", v.Status},
		{"scheduled-at", v.due},
		{"subject", v.Subject},
		{"recipients", v.Recipients},
	}}
	if v.MessageID != "" {
		table.Rows = append(table.Rows, []string{"message-id", v.MessageID})
	}
	if v.BatchID != "" {
		table.Rows = append(table.Rows, []string{"batch", v.BatchID})
	}
	if v.Topic != "" {
		table.Rows = append(table.Rows, []string{"topic", v.Topic})
	}
	if len(v.Tags) > 0 {
		table.Rows = append(table.Rows, []string{"tags", renderTags(v.Tags)})
	}

	lines := make([]string, 0, len(table.Rows)+1)
	for _, line := range table.Lines() {
		lines = append(lines, "  "+line)
	}
	return lines
}

// CancelView acknowledges one cancellation.
type CancelView struct {
	// ID is the cancelled send's id.
	ID string `json:"id"`
	// Status is the resulting status, always canceled.
	Status string `json:"status"`
}

// RenderText implements output.TextRenderer.
func (v CancelView) RenderText(caps output.Caps) []string {
	return []string{caps.Glyphs.OK + " Canceled " + v.ID}
}

// BatchView acknowledges an action on a whole batch.
type BatchView struct {
	// BatchID is the batch that was targeted.
	BatchID string `json:"batchId"`
	// Action is what was done: rescheduled or canceled.
	Action string `json:"action"`
	// Count is how many pending emails it affected. An unknown batch is a no-op reporting
	// zero rather than an error, which is worth showing rather than hiding.
	Count int `json:"count"`
	// DueAt is the new due time, on a reschedule.
	DueAt string `json:"dueAt,omitempty"`

	// dueHuman is DueAt rendered for a person.
	dueHuman string
}

// RenderText implements output.TextRenderer.
func (v BatchView) RenderText(caps output.Caps) []string {
	line := caps.Glyphs.OK + " " + capitalise(v.Action) +
		" " + strconv.Itoa(v.Count) + " " + plural(v.Count, "email") + " in batch " + v.BatchID
	if v.DueAt == "" {
		return []string{line}
	}
	return []string{line, "  due " + v.dueHuman}
}

// capitalise upper-cases the first letter of a word this package wrote itself.
func capitalise(word string) string {
	if word == "" {
		return word
	}
	return strings.ToUpper(word[:1]) + word[1:]
}

// pageView builds the listing view for one page.
func pageView(page *mailkube.ScheduledEmailPage) ListView {
	items := make([]ItemView, 0, len(page.Data))
	for i := range page.Data {
		items = append(items, itemView(&page.Data[i]))
	}

	return ListView{
		Items:      items,
		TotalCount: page.Pagination.TotalCount,
		Page:       currentPage(page),
		Pages:      pageCount(page, len(items)),
		HasMore:    page.HasMore(),
		Complete:   true,
	}
}

// currentPage is the page in hand, defaulting to the first when the server did not say.
func currentPage(page *mailkube.ScheduledEmailPage) int {
	if page.Pagination.CurrentPage > 0 {
		return page.Pagination.CurrentPage
	}
	return 1
}

// pageCount works out how many pages there are from the total and this page's size.
//
// It is derived rather than reported, because the API sends a total and a position but no page
// count, and it is only offered when the arithmetic is sound: a final short page would otherwise
// make the CLI announce a number that is wrong.
func pageCount(page *mailkube.ScheduledEmailPage, size int) int {
	total := page.Pagination.TotalCount
	if size == 0 || total == 0 {
		return 0
	}
	if !page.HasMore() && currentPage(page) == 1 {
		return 1
	}
	if !page.HasMore() {
		return currentPage(page)
	}
	return (total + size - 1) / size
}

// itemView builds one listing row.
//
// It takes no clock, unlike detailView: a row shows the instant, and a "in 2 hours" column that
// changed between two runs of the same command would make a listing impossible to diff.
func itemView(email *mailkube.ScheduledEmail) ItemView {
	return ItemView{
		ID:          email.ID,
		Status:      email.Status,
		ScheduledAt: email.ScheduledAt,
		Subject:     email.Subject,
		Recipients:  email.Recipients,
		BatchID:     email.BatchID,
		Topic:       email.Topic,
		due:         humanTime(email.ScheduledAt),
	}
}

// detailView builds the full view of one scheduled send.
func detailView(deps *feature.Deps, email *mailkube.ScheduledEmail) DetailView {
	return DetailView{
		ID:          email.ID,
		MessageID:   email.MessageID,
		Status:      email.Status,
		ScheduledAt: email.ScheduledAt,
		CreatedAt:   email.CreatedAt,
		Subject:     email.Subject,
		Recipients:  email.Recipients,
		BatchID:     email.BatchID,
		Topic:       email.Topic,
		Tags:        email.Tags,
		due:         dueText(deps.Clock.Now(), email.ScheduledAt),
	}
}

// short abbreviates an id for a table cell.
func short(id string) string {
	if len(id) <= shortIDLength {
		return id
	}
	return id[:shortIDLength]
}

// orDash renders an absent value as the glyph set's placeholder.
func orDash(value string, caps output.Caps) string {
	if value == "" {
		return caps.Glyphs.Dash
	}
	return value
}

// renderTags renders tags as name=value pairs, which is how they are written on the way in.
func renderTags(tags []mailkube.Tag) string {
	parts := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag.Value == "" {
			parts = append(parts, tag.Name)
			continue
		}
		parts = append(parts, tag.Name+"="+tag.Value)
	}
	return strings.Join(parts, ", ")
}

// humanTime renders an instant the way every screen in this CLI renders one.
//
// A value this release cannot parse is printed as it arrived. The server owns its timestamps,
// and a display that silently dropped one it did not recognise is worse than one showing an
// unfamiliar string.
func humanTime(value string) string {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return at.UTC().Format("2006-01-02 15:04") + " UTC"
}

// dueText renders a due time as an instant and as a distance from now.
func dueText(now time.Time, value string) string {
	human := humanTime(value)

	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return human
	}
	return human + "   (" + relative(now, at) + ")"
}

// relative renders the distance to an instant, in the past or the future.
func relative(from, to time.Time) string {
	d := to.Sub(from)
	if d < 0 {
		return "overdue"
	}

	switch {
	case d < 90*time.Second:
		return "in " + strconv.Itoa(rounded(d, time.Second)) + " seconds"
	case d < 90*time.Minute:
		return "in " + count(rounded(d, time.Minute), "minute")
	case d < 36*time.Hour:
		return "in " + count(rounded(d, time.Hour), "hour")
	default:
		return "in " + count(rounded(d, 24*time.Hour), "day")
	}
}

// rounded returns the duration in whole units, rounded to the nearest.
func rounded(d, unit time.Duration) int { return int((d + unit/2) / unit) }

// count renders a number with its unit, pluralised.
func count(n int, unit string) string { return strconv.Itoa(n) + " " + plural(n, unit) }

// plural returns the unit in the right number.
func plural(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return unit + "s"
}
