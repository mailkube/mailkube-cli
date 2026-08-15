package emails

import (
	"encoding/json"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// Transport is which way a message goes out.
type Transport string

// The two transports. Which one is used is always explicit, never inferred from which credential
// happens to be configured: the two are different principals, and guessing would mean a message
// silently leaving by a route the user did not choose.
const (
	// API is the REST transport, through the SDK.
	API Transport = "api"
	// SMTP is submission, through kernel/smtp.
	SMTP Transport = "smtp"
)

// parseTransport reads the --transport flag.
func parseTransport(value string) (Transport, error) {
	switch Transport(strings.ToLower(strings.TrimSpace(value))) {
	case API:
		return API, nil
	case SMTP:
		return SMTP, nil
	default:
		return "", errs.Usagef("unknown transport %q: use api or smtp", value)
	}
}

// The headers the platform reads on the submission path.
//
// Over REST these travel as body fields; over SMTP there is no body to put them in, so they travel
// as headers. This table is the mapping, and it is the only place it exists — the validation that
// forbids a user from setting these names itself lives with the payload rules, so the two cannot
// disagree about which names are the platform's.
const (
	headerTopic           = "X-Mailkube-Topic"
	headerTags            = "X-Mailkube-Tags"
	headerTemplateID      = "X-Mailkube-Template-Id"
	headerTemplateVersion = "X-Mailkube-Template-Version"
	headerVariables       = "X-Mailkube-Template-Variables"
)

// apiOnlyFlags are the send options the submission path has no equivalent for.
//
// Combining one with --transport smtp is a usage error rather than a silent drop, which is the
// whole point: a scheduled send that quietly went out immediately is the kind of failure someone
// discovers from a customer.
func apiOnlyFlags(p Payload, key string) []string {
	var named []string
	if p.ScheduledAt != "" {
		named = append(named, "--at")
	}
	if p.BatchID != "" {
		named = append(named, "--batch-id")
	}
	if key != "" {
		named = append(named, "--idempotency-key")
	}
	return named
}

// checkSMTPSupport refuses the flag combinations submission cannot honour.
func checkSMTPSupport(p Payload, key string) error {
	named := apiOnlyFlags(p, key)
	if len(named) == 0 {
		return nil
	}
	return errs.Usagef(
		"%s %s only supported on the api transport.\n"+
			"Scheduling and idempotency are properties of the API, not of submission.\n"+
			"Drop --transport smtp, or drop %s.",
		strings.Join(named, " and "),
		verb(len(named)),
		strings.Join(named, " and "))
}

// verb agrees the refusal's grammar with how many flags it names.
func verb(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// smtpMessage converts a payload into a submission message.
//
// This is the second emitter of the one payload builder: the same validated value that becomes a
// JSON body over REST becomes headers and MIME parts here, so a message that sends over one
// transport sends over the other unchanged.
func smtpMessage(deps *feature.Deps, p Payload) (mksmtp.Message, error) {
	headers, err := smtpHeaders(p)
	if err != nil {
		return mksmtp.Message{}, err
	}

	attachments, err := smtpAttachments(p)
	if err != nil {
		return mksmtp.Message{}, err
	}

	return mksmtp.Message{
		From:        p.From,
		To:          p.To,
		CC:          p.CC,
		BCC:         p.BCC,
		ReplyTo:     p.ReplyTo,
		Subject:     p.Subject,
		Text:        p.Text,
		HTML:        p.HTML,
		Headers:     headers,
		Attachments: attachments,
		Date:        deps.Clock.Now(),
	}, nil
}

// smtpHeaders renders the platform fields the submission path carries as headers.
func smtpHeaders(p Payload) (map[string]string, error) {
	headers := make(map[string]string, len(p.Headers)+4)
	for name, value := range p.Headers {
		headers[name] = value
	}

	if p.Topic != "" {
		headers[headerTopic] = p.Topic
	}
	if p.TemplateID != "" {
		headers[headerTemplateID] = p.TemplateID
	}
	if p.TemplateVersion != "" {
		headers[headerTemplateVersion] = p.TemplateVersion
	}
	if len(p.Tags) > 0 {
		encoded, err := json.Marshal(p.Tags)
		if err != nil {
			return nil, errs.WithCode(errs.CodeInternal, err)
		}
		headers[headerTags] = string(encoded)
	}
	if len(p.Variables) > 0 {
		encoded, err := json.Marshal(p.Variables)
		if err != nil {
			return nil, errs.WithCode(errs.CodeInternal, err)
		}
		headers[headerVariables] = string(encoded)
	}
	return headers, nil
}

// previewAttachments replaces attachment content with a note about its size.
//
// The JSON preview elides by substituting a placeholder for the base64, which works because that
// payload is only ever rendered. A MIME preview is built by the same code that builds a real
// message, so the placeholder has to be real content — otherwise the builder would try to decode
// a sentence as base64.
func previewAttachments(attachments []mksmtp.Attachment) []mksmtp.Attachment {
	preview := make([]mksmtp.Attachment, 0, len(attachments))
	for _, a := range attachments {
		preview = append(preview, mksmtp.Attachment{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Content:     []byte("<" + humanSize(len(a.Content)) + ", content omitted>"),
		})
	}
	return preview
}

// smtpAttachments decodes the payload's attachments into the bytes the builder encodes.
func smtpAttachments(p Payload) ([]mksmtp.Attachment, error) {
	if len(p.Attachments) == 0 {
		return nil, nil
	}

	decoded, err := decodeAttachments(p.Attachments)
	if err != nil {
		return nil, err
	}

	out := make([]mksmtp.Attachment, 0, len(decoded))
	for _, a := range decoded {
		out = append(out, mksmtp.Attachment{
			Filename: a.Filename, Content: a.Content, ContentType: a.ContentType,
		})
	}
	return out, nil
}
