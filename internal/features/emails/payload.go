package emails

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// Payload is the wire shape of a send, and the single place the CLI decides what a send looks
// like.
//
// It exists as its own type rather than as the SDK's parameter struct because three things have
// to agree on one shape: what `--json` accepts, what `--generate-skeleton` emits, and what
// `--dry-run` prints. The SDK's parameters carry Go field names and a time.Time; this carries the
// wire names and the wire spellings, which is what a user edits and what a reader of a dry run
// compares against the API documentation.
//
// The field order is the order every one of those three renders in. A test pins the key set
// against a request the SDK actually sent, so a field added upstream cannot go unnoticed here.
type Payload struct {
	From            string            `json:"from"`
	To              []string          `json:"to"`
	CC              []string          `json:"cc,omitempty"`
	BCC             []string          `json:"bcc,omitempty"`
	ReplyTo         []string          `json:"reply_to,omitempty"`
	Subject         string            `json:"subject"`
	HTML            string            `json:"html,omitempty"`
	Text            string            `json:"text,omitempty"`
	TemplateID      string            `json:"template_id,omitempty"`
	TemplateVersion string            `json:"template_version,omitempty"`
	Variables       map[string]string `json:"variables,omitempty"`
	Topic           string            `json:"topic,omitempty"`
	Tags            []Tag             `json:"tags,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Attachments     []Attachment      `json:"attachments,omitempty"`
	ScheduledAt     string            `json:"scheduled_at,omitempty"`
	BatchID         string            `json:"batch_id,omitempty"`
}

// Tag is one name/value label carried with the message.
type Tag struct {
	// Name is the tag name.
	Name string `json:"name"`
	// Value is the tag value, which may be empty.
	Value string `json:"value"`
}

// Attachment is one attached file, carrying its content base64-encoded as the wire does.
//
// Content stays a string rather than raw bytes because this type is what `--json` parses and what
// `--dry-run` prints, and both of those are the encoded form. Decoding happens once, on the way
// into the SDK.
type Attachment struct {
	// Filename is the name the recipient sees.
	Filename string `json:"filename"`
	// Content is the file's bytes, base64-encoded.
	Content string `json:"content"`
	// ContentType overrides the type the server would infer from the filename.
	ContentType string `json:"content_type,omitempty"`

	// elide replaces the content with its size when the payload is being shown rather than
	// sent. It lives on the type so that every rendering of an attachment elides identically:
	// a dry run that dumped megabytes of base64 into a terminal is not a preview, and one that
	// elided in text but not in JSON would make `--dry-run -o json` unusable for the diffing
	// it exists for.
	elide bool
}

// MarshalJSON implements json.Marshaler, honouring the elision.
func (a Attachment) MarshalJSON() ([]byte, error) {
	content := a.Content
	if a.elide {
		content = "<" + humanSize(len(a.Content)) + ", base64 omitted>"
	}
	// An anonymous struct rather than a second exported type: the shape is identical and the
	// only difference is which string lands in Content.
	//
	// Encoded without HTML escaping, because the placeholder is written in angle brackets and
	// json.Marshal would turn it into "<32 B, base64 omitted>". The escaping applied
	// inside a MarshalJSON survives whatever the outer encoder was configured with, so a
	// preview built the obvious way is unreadable at exactly the point it is meant to inform.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		Filename    string `json:"filename"`
		Content     string `json:"content"`
		ContentType string `json:"content_type,omitempty"`
	}{Filename: a.Filename, Content: content, ContentType: a.ContentType}); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Elided returns a copy of the payload whose attachments render as their size.
func (p Payload) Elided() Payload {
	if len(p.Attachments) == 0 {
		return p
	}

	elided := make([]Attachment, len(p.Attachments))
	for i, a := range p.Attachments {
		a.elide = true
		elided[i] = a
	}
	p.Attachments = elided
	return p
}

// EncodedSize is how many bytes the attachments occupy on the wire, already base64-encoded.
//
// It is the encoded figure rather than the file size because that is what is actually
// transmitted, and the difference is a third again: a user told their 15 MB file was fine, whose
// request is rejected at 20 MB, has been told the wrong thing.
func (p Payload) EncodedSize() int {
	total := 0
	for _, a := range p.Attachments {
		total += len(a.Content)
	}
	return total
}

// Params converts the payload into the SDK's send parameters.
//
// This is the only place the two shapes meet, so the wire spellings the user edits and the Go
// types the SDK takes stay each other's business and nobody else's.
func (p Payload) Params(key string) (mailkube.SendEmailParams, error) {
	attachments, err := decodeAttachments(p.Attachments)
	if err != nil {
		return mailkube.SendEmailParams{}, err
	}

	at, err := parseScheduledAt(p.ScheduledAt)
	if err != nil {
		return mailkube.SendEmailParams{}, err
	}

	return mailkube.SendEmailParams{
		From:            p.From,
		To:              p.To,
		Subject:         p.Subject,
		HTML:            p.HTML,
		Text:            p.Text,
		CC:              p.CC,
		BCC:             p.BCC,
		ReplyTo:         p.ReplyTo,
		Headers:         p.Headers,
		Attachments:     attachments,
		Tags:            tags(p.Tags),
		TemplateID:      p.TemplateID,
		TemplateVersion: p.TemplateVersion,
		Variables:       p.Variables,
		Topic:           p.Topic,
		IdempotencyKey:  key,
		ScheduledAt:     at,
		BatchID:         p.BatchID,
	}, nil
}

// decodeAttachments turns the encoded content back into the bytes the SDK encodes itself.
//
// The round trip is deliberate: the CLI's own shape is the wire's, so `--json` and `--dry-run`
// show what is sent, and the SDK stays the only thing that decides how bytes become base64.
func decodeAttachments(in []Attachment) ([]mailkube.Attachment, error) {
	if len(in) == 0 {
		return nil, nil
	}

	out := make([]mailkube.Attachment, 0, len(in))
	for _, a := range in {
		content, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			return nil, errs.Validationf(
				"attachment %q has content that is not valid base64: %v", a.Filename, err)
		}
		out = append(out, mailkube.Attachment{
			Filename: a.Filename, Content: content, ContentType: a.ContentType,
		})
	}
	return out, nil
}

// tags converts the CLI's tags into the SDK's.
func tags(in []Tag) []mailkube.Tag {
	if len(in) == 0 {
		return nil
	}

	out := make([]mailkube.Tag, 0, len(in))
	for _, t := range in {
		out = append(out, mailkube.Tag{Name: t.Name, Value: t.Value})
	}
	return out
}

// parseScheduledAt reads the wire spelling of a scheduling time.
//
// The value reaching here has already been through the flag parser or arrived inside a `--json`
// payload; either way it must carry an offset, for the reason the flag parser states.
func parseScheduledAt(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errs.Validationf(
			"scheduled_at %q is not an RFC 3339 time with an offset, for example %q",
			value, "2026-09-01T09:00:00Z")
	}
	return at, nil
}

// AutoKey is the value of --idempotency-key that means "derive one from this message".
const AutoKey = "auto"

// DeriveKey hashes the payload into a stable idempotency key.
//
// Hashing the body is what makes re-running the identical command a replay rather than a second
// charged message, and it matches how the server binds a key to the request it first saw. Two
// consequences are worth knowing rather than discovering: an intentionally repeated identical
// send inside the server's remembering window becomes a no-op, and changing one character of the
// body produces a different key and therefore a real second send.
func DeriveKey(p Payload) (string, error) {
	// Marshalling the payload rather than hashing field by field means a field added above is
	// covered by construction. Go emits struct fields in declaration order and map keys sorted,
	// so the same payload always produces the same bytes.
	encoded, err := json.Marshal(p)
	if err != nil {
		return "", errs.WithCode(errs.CodeInternal, err)
	}

	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
