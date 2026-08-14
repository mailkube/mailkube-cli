package emails_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/emails"
)

// valid is the smallest payload that passes, for the tests that break one thing about it.
func valid() emails.Payload {
	return emails.Payload{
		From:    "Acme <hello@acme.com>",
		To:      []string{"alice@example.com"},
		Subject: "Welcome",
		Text:    "Thanks for signing up.",
	}
}

func TestAWellFormedPayloadPasses(t *testing.T) {
	t.Parallel()

	if err := emails.Validate(valid()); err != nil {
		t.Errorf("a valid payload was rejected: %v", err)
	}
}

func TestValidationRefusesWhatIsCertainlyWrong(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// mutate breaks one property of an otherwise valid payload.
		mutate func(*emails.Payload)
		// wants is a phrase the message must carry, so the refusal is actionable rather
		// than merely correct.
		wants string
	}{
		{"no sender", func(p *emails.Payload) { p.From = "" }, "--from"},
		{"malformed sender", func(p *emails.Payload) { p.From = "not an address" }, "display name"},
		{"no recipient", func(p *emails.Payload) { p.To = nil }, "--to"},
		{"malformed recipient", func(p *emails.Payload) { p.To = []string{"alice at example"} }, "to address"},
		{"malformed cc", func(p *emails.Payload) { p.CC = []string{"@"} }, "cc address"},
		{"no subject", func(p *emails.Payload) { p.Subject = "" }, "--subject"},
		{
			"subject with a line break",
			func(p *emails.Payload) { p.Subject = "Hello\r\nBcc: someone@example.com" },
			"line break",
		},
		{
			"overlong subject",
			func(p *emails.Payload) { p.Subject = strings.Repeat("a", 256) },
			"maximum is 255",
		},
		{"no content", func(p *emails.Payload) { p.Text = "" }, "--html"},
		{
			"a template and a body",
			func(p *emails.Payload) { p.TemplateID = "t" },
			"one or the other",
		},
		{
			"a template version with no template",
			func(p *emails.Payload) { p.TemplateVersion = "latest" },
			"template_version",
		},
		{
			"variables with no template",
			func(p *emails.Payload) { p.Variables = map[string]string{"a": "b"} },
			"variables",
		},
		{"overlong topic", func(p *emails.Payload) { p.Topic = strings.Repeat("t", 17) }, "maximum is 16"},
		{
			"a tag name outside the vocabulary",
			func(p *emails.Payload) { p.Tags = []emails.Tag{{Name: "campaign name"}} },
			"A-Z a-z 0-9 _ -",
		},
		{
			"a tag value outside the vocabulary",
			func(p *emails.Payload) { p.Tags = []emails.Tag{{Name: "campaign", Value: "spring launch"}} },
			"Tag values allow",
		},
		{
			"a repeated tag name",
			func(p *emails.Payload) {
				p.Tags = []emails.Tag{{Name: "a", Value: "1"}, {Name: "a", Value: "2"}}
			},
			"more than once",
		},
		{
			"a header in the platform's namespace",
			func(p *emails.Payload) { p.Headers = map[string]string{"X-Mailkube-Topic": "news"} },
			"cannot be supplied",
		},
		{
			"a header carrying a line break",
			func(p *emails.Payload) { p.Headers = map[string]string{"X-A": "b\r\nX-C: d"} },
			"line break",
		},
		{
			"an attachment with no content",
			func(p *emails.Payload) { p.Attachments = []emails.Attachment{{Filename: "a.txt"}} },
			"no content",
		},
		{
			"an attachment with no name",
			func(p *emails.Payload) { p.Attachments = []emails.Attachment{{Content: "aGk="}} },
			"no filename",
		},
		{"a batch with no schedule", func(p *emails.Payload) { p.BatchID = "b" }, "--at"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := valid()
			tc.mutate(&p)

			err := emails.Validate(p)
			if err == nil {
				t.Fatal("the payload was accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("message = %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

func TestTheCasingOfAReservedHeaderDoesNotMatter(t *testing.T) {
	t.Parallel()

	// Header names are case-insensitive on the wire, so a rule that only caught one spelling
	// would be a rule anyone could step around by accident.
	p := valid()
	p.Headers = map[string]string{"x-MAILKUBE-tags": "[]"}

	if err := emails.Validate(p); err == nil {
		t.Error("a reserved header in a different casing was accepted")
	}
}

func TestTheCapsThatDependOnAPlanAreNotEnforcedLocally(t *testing.T) {
	t.Parallel()

	// Fifty-one recipients may be within this caller's plan or beyond it, and the CLI cannot
	// know which. Refusing here would produce a rejection the server would have accepted,
	// which is worse than the round trip it saves.
	p := valid()
	p.To = make([]string, 51)
	for i := range p.To {
		p.To[i] = "alice@example.com"
	}

	if err := emails.Validate(p); err != nil {
		t.Errorf("a recipient count the CLI cannot judge was refused locally: %v", err)
	}
}

func TestATagMayCarryANameAlone(t *testing.T) {
	t.Parallel()

	p := valid()
	p.Tags = []emails.Tag{{Name: "beta"}}

	if err := emails.Validate(p); err != nil {
		t.Errorf("a valueless tag was rejected: %v", err)
	}
}
