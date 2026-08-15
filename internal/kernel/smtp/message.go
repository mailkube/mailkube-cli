// Package smtp composes and submits mail over SMTP.
//
// It is the one place the CLI speaks submission, and it speaks it with the standard library:
// net/smtp for the protocol, and mime, mime/multipart, mime/quotedprintable and net/mail for
// composition. No SDK is involved, and that is the point — an SDK exists to hold what is specific
// to Mailkube, and SMTP is a standard protocol that every language already has a library for.
//
// What *is* specific to Mailkube on this path — which headers a customer may set, what shape a
// username takes — is product policy and lives with the equivalent rules for the REST path, not
// in here. This package knows how to build a message and put it on a wire, and nothing else.
package smtp

import (
	"bytes"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"sort"
	"strings"
	"time"
)

// Message is one message to submit.
//
// The addresses are strings rather than parsed values because that is what a command line carries
// and what the REST payload carries; parsing happens here, once, so both transports accept exactly
// the same spellings.
type Message struct {
	// From is the sender, optionally with a display name.
	From string
	// To, CC and BCC are the recipients. BCC is used for the envelope and never written as a
	// header, which is the whole of what "blind" means.
	To, CC, BCC []string
	// ReplyTo are the Reply-To addresses.
	ReplyTo []string
	// Subject is the subject line.
	Subject string
	// Text and HTML are the body parts. Either may be empty; both together produce a
	// multipart/alternative.
	Text, HTML string
	// Headers are extra headers, already validated by the caller.
	Headers map[string]string
	// Attachments are the files to attach.
	Attachments []Attachment
	// Date is the message date. Zero means the caller forgot, and Build says so rather than
	// reaching for the wall clock: a package that reads a clock cannot be golden-tested.
	Date time.Time
	// MessageIDDomain, when set, is used to generate a Message-ID.
	//
	// Empty is the normal case and means no Message-ID header is written, because the platform
	// assigns one. Writing our own would be a second id for one message.
	MessageIDDomain string
}

// Attachment is one file to attach.
type Attachment struct {
	// Filename is the name the recipient sees.
	Filename string
	// Content is the raw bytes; this package base64-encodes them.
	Content []byte
	// ContentType is the MIME type. Empty falls back to a generic binary type rather than
	// guessing from the extension, because a wrong type is worse than an honest one.
	ContentType string
}

// Recipients is every address the envelope must carry, including the blind ones.
//
// This is separate from the headers on purpose. BCC recipients receive the message because they
// are named in the envelope, and they stay private because they are not named in the headers, and
// conflating the two is how a blind copy stops being blind.
func (m Message) Recipients() []string {
	all := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	all = append(all, m.To...)
	all = append(all, m.CC...)
	return append(all, m.BCC...)
}

// Sender is the envelope sender: the address alone, without any display name.
func (m Message) Sender() (string, error) {
	parsed, err := mail.ParseAddress(m.From)
	if err != nil {
		return "", fmt.Errorf("invalid from address %q: %w", m.From, err)
	}
	return parsed.Address, nil
}

// Build renders the message as RFC 5322 bytes.
//
// It performs no I/O and reads no clock, so the same message always produces the same bytes and
// can be pinned by a golden file. Every encoding decision below is the standard library's:
// mime.QEncoding and BEncoding emit RFC 2047 encoded-words with the folding rule,
// net/mail.Address encodes a display name while leaving the addr-spec untouched, and
// mime.FormatMediaType emits an RFC 2231 filename* for a non-ASCII filename and a plain filename
// for an ASCII one.
func (m Message) Build() ([]byte, error) {
	if m.Date.IsZero() {
		return nil, fmt.Errorf("message has no date: pass the time from the injected clock")
	}

	headers, err := m.headerFields()
	if err != nil {
		return nil, err
	}

	var body bytes.Buffer
	contentHeaders, err := m.writeBody(&body)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	writeHeaders(&out, append(headers, contentHeaders...))
	out.WriteString("\r\n")
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// header is one rendered header field, kept ordered rather than mapped.
//
// Order matters to a reader diffing two messages and to a golden file, and a map would make it
// arbitrary. It is not significant to a mail server, which is exactly why leaving it to chance
// would go unnoticed until someone compared two runs.
type header struct{ name, value string }

// headerFields renders every header except the content ones, which depend on the body.
func (m Message) headerFields() ([]header, error) {
	from, err := formatAddresses([]string{m.From})
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := formatAddresses(m.To)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}

	fields := []header{
		{"From", from},
		{"To", to},
		{"Subject", encodeHeaderValue(m.Subject)},
		{"Date", m.Date.Format(time.RFC1123Z)},
		{"MIME-Version", "1.0"},
	}

	// CC is written; BCC is deliberately not. The envelope carries the blind recipients, and a
	// BCC header would disclose them to everyone else on the message.
	if optional, err := optionalAddressHeader("Cc", m.CC); err != nil {
		return nil, err
	} else if optional != nil {
		fields = insertAfter(fields, "To", *optional)
	}
	if optional, err := optionalAddressHeader("Reply-To", m.ReplyTo); err != nil {
		return nil, err
	} else if optional != nil {
		fields = append(fields, *optional)
	}

	if m.MessageIDDomain != "" {
		fields = append(fields, header{"Message-ID", "<" + messageID(m) + "@" + m.MessageIDDomain + ">"})
	}
	return append(fields, m.customHeaders()...), nil
}

// customHeaders renders the caller's extra headers, in a stable order.
func (m Message) customHeaders() []header {
	names := make([]string, 0, len(m.Headers))
	for name := range m.Headers {
		names = append(names, name)
	}
	sort.Strings(names)

	fields := make([]header, 0, len(names))
	for _, name := range names {
		fields = append(fields, header{textproto.CanonicalMIMEHeaderKey(name), encodeHeaderValue(m.Headers[name])})
	}
	return fields
}

// optionalAddressHeader renders an address header, or nothing when the list is empty.
func optionalAddressHeader(name string, addresses []string) (*header, error) {
	if len(addresses) == 0 {
		return nil, nil
	}

	rendered, err := formatAddresses(addresses)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.ToLower(name), err)
	}
	return &header{name, rendered}, nil
}

// insertAfter places a field directly after a named one, keeping the reading order conventional.
func insertAfter(fields []header, after string, field header) []header {
	for i, existing := range fields {
		if existing.name != after {
			continue
		}
		out := make([]header, 0, len(fields)+1)
		out = append(out, fields[:i+1]...)
		out = append(out, field)
		return append(out, fields[i+1:]...)
	}
	return append(fields, field)
}

// formatAddresses parses and re-renders an address list.
//
// Re-rendering rather than passing the input through is what encodes a non-ASCII display name
// correctly, and it is also a validation: an address that cannot be parsed here would have been
// rejected by the server after the whole message was uploaded.
func formatAddresses(addresses []string) (string, error) {
	rendered := make([]string, 0, len(addresses))
	for _, address := range addresses {
		parsed, err := mail.ParseAddress(address)
		if err != nil {
			return "", fmt.Errorf("invalid address %q: %w", address, err)
		}
		rendered = append(rendered, parsed.String())
	}
	return strings.Join(rendered, ", "), nil
}

// encodeHeaderValue encodes a header value when it needs it, and leaves it alone when it does not.
//
// Encoding unconditionally would turn every plain English subject into an unreadable encoded-word
// in any tool that shows raw headers, which is most of the tools someone debugging a send reaches
// for.
func encodeHeaderValue(value string) string {
	if isASCII(value) {
		return value
	}
	return mime.QEncoding.Encode("utf-8", value)
}

// isASCII reports whether every byte is printable US-ASCII, which is what a header field body may
// carry unencoded.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] > 0x7e || s[i] < 0x20 {
			return false
		}
	}
	return true
}

// writeHeaders renders the header block, folding nothing itself.
//
// The encoders above already fold what they encode, and an unencoded ASCII value is the caller's
// to keep within the line limit — which the validation on the way in already enforces.
func writeHeaders(out *bytes.Buffer, fields []header) {
	for _, field := range fields {
		out.WriteString(field.name)
		out.WriteString(": ")
		out.WriteString(field.value)
		out.WriteString("\r\n")
	}
}

// messageID derives a stable id from the message's own content.
//
// Stable rather than random, so a golden file can pin it. This is only reached when a caller asks
// for a Message-ID at all, which the send path does not: the platform assigns one.
func messageID(m Message) string {
	sum := 0
	for _, part := range []string{m.From, m.Subject, m.Date.Format(time.RFC3339Nano)} {
		for i := range len(part) {
			sum = sum*31 + int(part[i])
		}
	}
	return fmt.Sprintf("%016x", uint64(sum)) //nolint:gosec // an id, not a checksum
}

// writeBody writes the body and returns the content headers describing it.
//
// Three shapes, in order of how common they are: one part, two alternative parts, and either of
// those wrapped in a mixed part carrying attachments. Each is assembled by the standard library's
// multipart writer, so the boundaries and the part headers are its problem rather than ours.
func (m Message) writeBody(out *bytes.Buffer) ([]header, error) {
	if len(m.Attachments) == 0 {
		return m.writeContent(out)
	}

	mixed := multipart.NewWriter(out)
	if err := m.writeContentPart(mixed); err != nil {
		return nil, err
	}
	for _, attachment := range m.Attachments {
		if err := writeAttachment(mixed, attachment); err != nil {
			return nil, err
		}
	}
	if err := mixed.Close(); err != nil {
		return nil, err
	}

	return []header{{"Content-Type", `multipart/mixed; boundary="` + mixed.Boundary() + `"`}}, nil
}

// writeContent writes the body when there is nothing to attach.
func (m Message) writeContent(out *bytes.Buffer) ([]header, error) {
	if m.Text != "" && m.HTML != "" {
		alternative := multipart.NewWriter(out)
		if err := writeAlternativeParts(alternative, m.Text, m.HTML); err != nil {
			return nil, err
		}
		if err := alternative.Close(); err != nil {
			return nil, err
		}
		return []header{
			{"Content-Type", `multipart/alternative; boundary="` + alternative.Boundary() + `"`},
		}, nil
	}

	content, mediaType := m.singlePart()
	encoded, err := encodeBody(content)
	if err != nil {
		return nil, err
	}
	out.Write(encoded)

	return []header{
		{"Content-Type", mediaType + "; charset=utf-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
	}, nil
}

// singlePart picks the one body part and its type.
func (m Message) singlePart() (content, mediaType string) {
	if m.HTML != "" {
		return m.HTML, "text/html"
	}
	return m.Text, "text/plain"
}

// writeContentPart writes the body as a part inside a mixed message.
func (m Message) writeContentPart(mixed *multipart.Writer) error {
	if m.Text == "" || m.HTML == "" {
		content, mediaType := m.singlePart()
		return writePart(mixed, mediaType+"; charset=utf-8", content)
	}

	// A nested multipart/alternative, because the alternatives are alternatives to each other
	// and not to the attachments.
	var nested bytes.Buffer
	alternative := multipart.NewWriter(&nested)
	if err := writeAlternativeParts(alternative, m.Text, m.HTML); err != nil {
		return err
	}
	if err := alternative.Close(); err != nil {
		return err
	}

	part, err := mixed.CreatePart(textproto.MIMEHeader{
		"Content-Type": {`multipart/alternative; boundary="` + alternative.Boundary() + `"`},
	})
	if err != nil {
		return err
	}
	_, err = part.Write(nested.Bytes())
	return err
}

// writeAlternativeParts writes the plain-text part before the HTML one.
//
// The order is the specification's and it is load-bearing: a client shows the last part it can
// render, so reversing them shows plain text to everyone.
func writeAlternativeParts(w *multipart.Writer, text, html string) error {
	if err := writePart(w, "text/plain; charset=utf-8", text); err != nil {
		return err
	}
	return writePart(w, "text/html; charset=utf-8", html)
}

// writePart writes one quoted-printable body part.
func writePart(w *multipart.Writer, contentType, content string) error {
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}

	encoded, err := encodeBody(content)
	if err != nil {
		return err
	}
	_, err = part.Write(encoded)
	return err
}

// encodeBody renders a body part as quoted-printable.
//
// Quoted-printable rather than relying on the server advertising 8BITMIME: a message that assumes
// an extension and meets a relay without it arrives mangled, and the cost of encoding is that
// mostly-ASCII text stays readable in the raw source.
func encodeBody(content string) ([]byte, error) {
	var out bytes.Buffer
	writer := quotedprintable.NewWriter(&out)

	if _, err := writer.Write([]byte(content)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// defaultAttachmentType is what an attachment with no stated type is sent as.
//
// A generic binary type rather than a guess from the extension: the caller may state a type, and
// where they have not, an honest "I do not know" leaves the recipient's client to sniff, whereas a
// wrong specific type actively misleads it.
const defaultAttachmentType = "application/octet-stream"

// writeAttachment writes one base64 attachment part.
func writeAttachment(w *multipart.Writer, attachment Attachment) error {
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = defaultAttachmentType
	}

	// FormatMediaType is what produces filename*=utf-8''… for a non-ASCII name and a plain
	// filename= for an ASCII one, which is the whole of the RFC 2231 requirement.
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename})
	if disposition == "" {
		return fmt.Errorf("attachment filename %q cannot be written as a header parameter", attachment.Filename)
	}

	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType},
		"Content-Transfer-Encoding": {"base64"},
		"Content-Disposition":       {disposition},
	})
	if err != nil {
		return err
	}

	_, err = part.Write(wrapBase64(attachment.Content))
	return err
}
