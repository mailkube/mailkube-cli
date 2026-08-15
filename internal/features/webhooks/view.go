package webhooks

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

const (
	// idWidth is how much of an identifier a table column shows.
	//
	// Eight characters is enough to tell two ids apart by eye and short enough to leave room
	// for the value a reader is actually scanning for. The full id is in every machine format,
	// because a truncated id in something a user copies is a command that fails.
	idWidth = 8
	// typeWidth is the event-type column. The longest modelled type is wider than this and is
	// allowed to push the row rather than be cut: an event type is not a value to abbreviate.
	typeWidth = 17
	// continuation indents a line that belongs to the one above it.
	continuation = "       "
)

// EventView is one delivery, as both a rendered line and a machine record.
type EventView struct {
	// ID is the delivery id, in full. It is stable across retries, so it is what a caller
	// deduplicates on downstream.
	ID string `json:"id"`
	// Type is the event type.
	Type string `json:"type"`
	// EmailID is the message this event is about, when it is about one.
	EmailID string `json:"email_id,omitempty"`
	// Recipient is the address this event concerns.
	Recipient string `json:"recipient,omitempty"`
	// ReceivedAt is when this machine received it.
	ReceivedAt string `json:"received_at"`
	// Verified reports whether the signature was checked.
	Verified bool `json:"verified"`
	// Payload is the delivered body, verbatim.
	//
	// The typed fields above are this release's reading of it and drop anything newer. A
	// consumer that stores or forwards an event wants what arrived, at every nesting depth,
	// so the whole body travels rather than a reconstruction of it.
	Payload json.RawMessage `json:"payload"`

	// at is the reception time, kept as a time for the human form's clock column.
	at time.Time
	// since is how long ago this message was first heard of, zero when this is the first
	// event for it or when no clock is advancing.
	since time.Duration
	// code and reason are the receiving server's verdict, on the events that carry one.
	code   int
	reason string
	// width is the widest an externally-sourced field may render.
	width int
}

// RenderText implements output.TextRenderer.
//
// Every value on the line except the badge and the clock came from the delivered body, so every
// one of them is sanitised before it reaches a terminal. That includes the event type and the
// message id, which look like values this program chose and are not: with --skip-verify anyone
// can post a body, and a verified body is still text a sender supplied.
func (v EventView) RenderText(caps output.Caps) []string {
	line := fmt.Sprintf("  %s  %s  %s %s  %s",
		badge(v.Type, caps.Glyphs),
		v.at.UTC().Format("15:04:05"),
		pad(output.Sanitize(v.Type), typeWidth),
		pad(shorten(output.Sanitize(v.EmailID)), idWidth),
		output.Field(v.Recipient, v.width, caps.Glyphs.Ellipsis))

	// Only once there is a whole second to report. Two events arriving in the same instant are
	// the normal case on a fast path, and "(0s)" beside one of them reads as a measurement
	// rather than as the absence of one.
	if v.since >= time.Second {
		line += "  (" + elapsed(v.since) + ")"
	}
	if !v.Verified {
		line += "  UNVERIFIED"
	}

	lines := []string{line}
	if v.reason != "" || v.code != 0 {
		lines = append(lines, continuation+failure(v.code, output.Field(v.reason, v.width, caps.Glyphs.Ellipsis)))
	}
	return lines
}

// badge is the marker a reader scans down the left of the stream.
//
// Three states rather than one per event type: this went well, this went wrong, and this needs
// your attention. A reader watching a stream is looking for the second and third.
func badge(eventType string, glyphs output.Glyphs) string {
	switch eventType {
	case mailkube.EventTypeEmailBounced, mailkube.EventTypeEmailFailed:
		return glyphs.Cross
	case mailkube.EventTypeEmailDeliveryDelayed, mailkube.EventTypeEmailSuppressed,
		mailkube.EventTypeDomainStatus, mailkube.EventTypeWebhookStatus:
		return glyphs.Warn
	default:
		return glyphs.OK
	}
}

// failure renders a receiving server's verdict.
func failure(code int, reason string) string {
	if code == 0 {
		return reason
	}
	if reason == "" {
		return fmt.Sprintf("code %d", code)
	}
	return fmt.Sprintf("code %d  %s", code, reason)
}

// detail is what the view needs out of an event's typed payload.
type detail struct {
	// emailID is the message the event is about.
	emailID string
	// who is the address or endpoint the event concerns.
	who string
	// code and reason are a delivery verdict, where there is one.
	code   int
	reason string
}

// detailOf reads an event's payload into the few fields a stream line shows.
//
// Three functions rather than one switch, split along the shapes the payloads actually have: an
// outcome for one recipient, an event about a message as a whole, and an event about neither.
// A single switch over all eleven types would read as one thing while doing three.
func detailOf(data any) detail {
	if d, ok := outcomeDetail(data); ok {
		return d
	}
	if d, ok := messageDetail(data); ok {
		return d
	}
	return endpointDetail(data)
}

// outcomeDetail reads the events that report what happened to one recipient.
func outcomeDetail(data any) (detail, bool) {
	switch d := data.(type) {
	case *mailkube.SentData:
		return detail{emailID: d.EmailID, who: d.Sent.Recipient}, true
	case *mailkube.DeliveredData:
		return detail{emailID: d.EmailID, who: d.Delivery.Recipient}, true
	case *mailkube.BouncedData:
		return detail{emailID: d.EmailID, who: d.Bounce.Recipient, code: d.Bounce.Code, reason: d.Bounce.Reason}, true
	case *mailkube.DelayedData:
		return detail{emailID: d.EmailID, who: d.Delay.Recipient, code: d.Delay.Code, reason: d.Delay.Reason}, true
	case *mailkube.SuppressedData:
		return detail{emailID: d.EmailID, who: strings.Join(d.Suppression.Recipients, ", ")}, true
	default:
		return detail{}, false
	}
}

// messageDetail reads the events that are about a message rather than one delivery of it.
func messageDetail(data any) (detail, bool) {
	switch d := data.(type) {
	case *mailkube.ScheduledData:
		return detail{emailID: d.EmailID, who: strings.Join(d.To, ", ")}, true
	case *mailkube.FailedData:
		return detail{emailID: d.EmailID, who: strings.Join(d.To, ", "), reason: d.Failed.Reason}, true
	case *mailkube.OpenedData:
		return detail{emailID: d.EmailID, who: strings.Join(d.To, ", ")}, true
	case *mailkube.ClickedData:
		return detail{emailID: d.EmailID, who: strings.Join(d.To, ", "), reason: d.Click.Link}, true
	default:
		return detail{}, false
	}
}

// endpointDetail reads the events that concern a domain or an endpoint, and anything unmodelled.
//
// An event type this release has never heard of still renders: it has a type, an id and a body,
// which is enough for a reader to see that it arrived and for a pipe to carry it on.
func endpointDetail(data any) detail {
	switch d := data.(type) {
	case *mailkube.DomainStatusData:
		return detail{who: d.Domain, reason: d.Status}
	case *mailkube.WebhookStatusData:
		return detail{who: d.EndpointURL, reason: d.DisabledReason}
	default:
		return detail{}
	}
}

// shorten abbreviates an identifier for a column.
func shorten(id string) string {
	if len(id) <= idWidth {
		return id
	}
	return id[:idWidth]
}

// pad widens a value to a column, leaving anything longer as it is.
//
// Display width rather than byte or rune count, for the same reason every other column in this
// CLI uses it: a value carrying a wide character would otherwise be padded to a column it has
// already overrun.
func pad(value string, width int) string {
	shortfall := width - output.DisplayWidth(value)
	if shortfall <= 0 {
		return value
	}
	return value + strings.Repeat(" ", shortfall)
}

// elapsed renders how long something took, at second resolution.
func elapsed(d time.Duration) string { return d.Round(time.Second).String() }
