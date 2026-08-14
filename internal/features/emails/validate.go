package emails

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// The caps checked locally.
//
// Every one of these is a property of the platform rather than of a plan, which is the whole test
// for whether a rule belongs here. A per-plan ceiling — how many recipients, how large a message,
// how fast you may send — is deliberately absent: the CLI cannot know the number, and guessing it
// produces a refusal the server would have accepted, which is worse than the round trip.
const (
	maxSubject     = 255
	maxTopic       = 16
	maxTags        = 20
	maxTagName     = 16
	maxTagValue    = 32
	maxHeaders     = 20
	maxHeaderName  = 64
	maxHeaderValue = 998
)

// reservedHeaderPrefix is the header namespace the platform owns.
//
// A user-supplied header may not enter it. The rule is stated as a prefix rather than as a list
// of names because the point is that this space is not the caller's, and because a list would
// have to be kept in step with the platform forever.
const reservedHeaderPrefix = "x-mailkube-"

// Validate checks everything that can be known without asking the server.
//
// The division of labour is the same everywhere in this CLI: shape, charset and mutual exclusion
// are decided here, because they are certainties and a round trip to learn one is a round trip
// wasted; anything that depends on the caller's plan or on server state is decided there.
func Validate(p Payload) error {
	for _, check := range []func(Payload) error{
		validateAddresses,
		validateSubject,
		validateContent,
		validateTopic,
		validateTags,
		validateHeaders,
		validateAttachments,
		validateScheduling,
	} {
		if err := check(p); err != nil {
			return err
		}
	}
	return nil
}

// validateAddresses checks the sender and every recipient list.
func validateAddresses(p Payload) error {
	if strings.TrimSpace(p.From) == "" {
		return errs.Validationf("no sender: pass --from, for example --from \"Acme <hello@acme.com>\"")
	}
	if err := validateAddress("from", p.From); err != nil {
		return err
	}
	if len(p.To) == 0 {
		return errs.Validationf("no recipient: pass --to")
	}

	lists := []struct {
		field     string
		addresses []string
	}{
		{"to", p.To}, {"cc", p.CC}, {"bcc", p.BCC}, {"reply_to", p.ReplyTo},
	}
	for _, list := range lists {
		for _, address := range list.addresses {
			if err := validateAddress(list.field, address); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateAddress checks one address, accepting the display-name form.
func validateAddress(field, address string) error {
	if strings.TrimSpace(address) == "" {
		return errs.Validationf("%s contains an empty address", field)
	}
	if _, err := mail.ParseAddress(address); err != nil {
		return errs.Validationf(
			"%s address %q is not a valid address\n"+
				"Write it as hello@acme.com, or as a display name: \"Acme <hello@acme.com>\".",
			field, address)
	}
	return nil
}

// validateSubject checks the one header the caller always sets.
func validateSubject(p Payload) error {
	if strings.TrimSpace(p.Subject) == "" {
		return errs.Validationf("no subject: pass --subject")
	}
	if containsBreak(p.Subject) {
		return errs.Validationf("the subject contains a line break, which is not allowed in a header")
	}
	if n := utf8.RuneCountInString(p.Subject); n > maxSubject {
		return errs.Validationf("the subject is %d characters; the maximum is %d", n, maxSubject)
	}
	return nil
}

// validateContent enforces the one-source-of-content rule.
//
// A send carries raw content or a saved template, never both: with a template id present the
// server renders the template, so an html body sent alongside it is silently ignored, and a
// caller who believed they had sent it would have no way to find out.
func validateContent(p Payload) error {
	hasRaw := p.HTML != "" || p.Text != ""

	switch {
	case p.TemplateID != "" && hasRaw:
		return errs.Validationf(
			"template_id cannot be combined with html or text: a send carries one or the other")
	case p.TemplateID == "" && !hasRaw:
		return errs.Validationf(
			"no content: pass --html, --text or --template-id (or --sample for a generated body)")
	case p.TemplateID == "" && p.TemplateVersion != "":
		return errs.Validationf("template_version has no effect without template_id")
	case p.TemplateID == "" && len(p.Variables) > 0:
		return errs.Validationf("variables have no effect without template_id")
	default:
		return nil
	}
}

// validateTopic checks the mailing-list topic slug.
func validateTopic(p Payload) error {
	if p.Topic == "" {
		return nil
	}
	if n := utf8.RuneCountInString(p.Topic); n > maxTopic {
		return errs.Validationf("the topic is %d characters; the maximum is %d", n, maxTopic)
	}
	return nil
}

// validateTags checks the tag vocabulary and the uniqueness of names.
func validateTags(p Payload) error {
	if len(p.Tags) > maxTags {
		return errs.Validationf("%d tags were given; the maximum is %d", len(p.Tags), maxTags)
	}

	seen := make(map[string]bool, len(p.Tags))
	for _, tag := range p.Tags {
		if err := validateTag(tag); err != nil {
			return err
		}
		if seen[tag.Name] {
			return errs.Validationf("tag %q is given more than once; tag names must be unique", tag.Name)
		}
		seen[tag.Name] = true
	}
	return nil
}

// validateTag checks one tag's name and value.
func validateTag(tag Tag) error {
	switch {
	case tag.Name == "":
		return errs.Validationf("a tag has no name: write it as --tag campaign=launch")
	case !isTagToken(tag.Name):
		return errs.Validationf(
			"invalid tag name %q\nTag names allow A-Z a-z 0-9 _ - only, max %d characters.",
			tag.Name, maxTagName)
	case utf8.RuneCountInString(tag.Name) > maxTagName:
		return errs.Validationf(
			"invalid tag name %q\nTag names allow A-Z a-z 0-9 _ - only, max %d characters.",
			tag.Name, maxTagName)
	case !isTagToken(tag.Value):
		return errs.Validationf(
			"invalid value %q for tag %q\nTag values allow A-Z a-z 0-9 _ - only, max %d characters.",
			tag.Value, tag.Name, maxTagValue)
	case utf8.RuneCountInString(tag.Value) > maxTagValue:
		return errs.Validationf(
			"invalid value %q for tag %q\nTag values allow A-Z a-z 0-9 _ - only, max %d characters.",
			tag.Value, tag.Name, maxTagValue)
	default:
		return nil
	}
}

// isTagToken reports whether every character is in the tag vocabulary. An empty value passes,
// because a tag may carry a name alone.
func isTagToken(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// validateHeaders checks the custom headers, including the namespace the caller may not write.
func validateHeaders(p Payload) error {
	if len(p.Headers) > maxHeaders {
		return errs.Validationf("%d headers were given; the maximum is %d", len(p.Headers), maxHeaders)
	}

	for name, value := range p.Headers {
		if err := validateHeader(name, value); err != nil {
			return err
		}
	}
	return nil
}

// validateHeader checks one header name and value.
//
// The reserved-namespace refusal names only the header the caller supplied. That is deliberate:
// the rule is that the namespace belongs to the platform, and listing what lives inside it would
// be a different statement.
func validateHeader(name, value string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errs.Validationf("a header has no name: write it as --header 'X-Campaign: launch'")
	case containsBreak(name) || containsBreak(value):
		return errs.Validationf("header %q contains a line break, which is not allowed", name)
	case utf8.RuneCountInString(name) > maxHeaderName:
		return errs.Validationf("header name %q is longer than %d characters", name, maxHeaderName)
	case utf8.RuneCountInString(value) > maxHeaderValue:
		return errs.Validationf("the value of header %q is longer than %d characters", name, maxHeaderValue)
	case strings.HasPrefix(strings.ToLower(name), reservedHeaderPrefix):
		return errs.Validationf(
			"header %q is set by the platform and cannot be supplied\n"+
				"Use the dedicated flags — --topic, --tag, --template-id — for what travels there.",
			name)
	default:
		return nil
	}
}

// validateAttachments checks that each attachment can be identified and has content.
func validateAttachments(p Payload) error {
	for _, a := range p.Attachments {
		switch {
		case strings.TrimSpace(a.Filename) == "":
			return errs.Validationf("an attachment has no filename")
		case containsBreak(a.Filename):
			return errs.Validationf("attachment filename %q contains a line break", a.Filename)
		case a.Content == "":
			return errs.Validationf("attachment %q has no content", a.Filename)
		}
	}
	return nil
}

// validateScheduling checks the pair of fields that only mean something together.
func validateScheduling(p Payload) error {
	if p.BatchID != "" && p.ScheduledAt == "" {
		return errs.Validationf(
			"batch_id groups scheduled sends, so it needs a scheduling time as well: pass --at")
	}
	return nil
}

// containsBreak reports whether a value carries a carriage return or newline.
//
// Both are checked rather than just the pair, because a header value is terminated by either on
// some parsers, and a value that can end its own header can start another one.
func containsBreak(s string) bool {
	return strings.ContainsAny(s, "\r\n")
}
