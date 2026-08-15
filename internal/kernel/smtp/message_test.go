package smtp_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/golden"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// fixedDate is the date every fixture carries, so a golden file pins the message and not the day
// it was generated.
func fixedDate() time.Time { return time.Date(2026, 8, 14, 7, 32, 0, 0, time.UTC) }

// boundary matches the random multipart boundary the standard library generates.
//
// It is the one part of a built message that legitimately differs per run, so it is replaced with
// a marker before the comparison. Fixing the boundary instead would mean reaching inside the
// multipart writer, and a test that rewrites the thing it is testing proves less.
func boundary() *regexp.Regexp { return regexp.MustCompile(`[0-9a-f]{60}`) }

// build renders a message and stabilises its boundaries.
func build(t *testing.T, m mksmtp.Message) []byte {
	t.Helper()

	raw, err := m.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return boundary().ReplaceAll(raw, []byte("BOUNDARY"))
}

// TestMessages pins the wire form of a message, including every encoding rule.
//
// These are the fixtures the encoding contract lives in. Each case exists because getting it
// wrong mangles mail for a whole class of user and does so silently: the send succeeds, and only
// the recipient sees the damage.
func TestMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message mksmtp.Message
	}{
		{
			name: "plain_text",
			message: mksmtp.Message{
				From: "Acme <hello@acme.com>", To: []string{"alice@example.com"},
				Subject: "Welcome", Text: "Thanks for signing up.\n", Date: fixedDate(),
			},
		},
		{
			name: "alternative_parts",
			message: mksmtp.Message{
				From: "hello@acme.com", To: []string{"alice@example.com"},
				Subject: "Welcome",
				Text:    "Thanks for signing up.\n", HTML: "<p>Thanks for signing up.</p>",
				Date: fixedDate(),
			},
		},
		{
			// Every non-ASCII case in one message, because they interact: an encoded
			// subject, an encoded display name whose addr-spec must stay untouched, a
			// body that has to be quoted-printable, and an RFC 2231 filename.
			name: "non_ascii",
			message: mksmtp.Message{
				From:    "Ünter Müller <hello@acme.com>",
				To:      []string{"Élodie <elodie@example.com>"},
				CC:      []string{"cc@example.com"},
				Subject: "Réunion trimestrielle — très important",
				Text:    "Bonjour,\n\nLa réunion est à 9h. À bientôt !\n",
				HTML:    "<p>La réunion est à 9h. À bientôt&nbsp;!</p>",
				Attachments: []mksmtp.Attachment{{
					Filename:    "rapport-financière.txt",
					Content:     []byte("chiffre d'affaires: 1 234 €\n"),
					ContentType: "text/plain; charset=utf-8",
				}},
				Date: fixedDate(),
			},
		},
		{
			name: "attachment_only",
			message: mksmtp.Message{
				From: "hello@acme.com", To: []string{"alice@example.com"},
				Subject: "Your report", Text: "Attached.\n",
				Attachments: []mksmtp.Attachment{{
					Filename: "report.pdf",
					Content:  []byte("%PDF-1.4 not really a pdf"),
				}},
				Date: fixedDate(),
			},
		},
		{
			name: "custom_headers",
			message: mksmtp.Message{
				From: "hello@acme.com", To: []string{"alice@example.com"},
				Subject: "Tagged", Text: "body\n",
				Headers: map[string]string{
					"x-campaign":       "launch",
					"X-Mailkube-Topic": "news",
					"In-Reply-To":      "<abc@msg.example.com>",
				},
				Date: fixedDate(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			golden.Assert(t, tc.name+".eml", build(t, tc.message))
		})
	}
}

func TestABlindCopyIsNotWrittenAsAHeader(t *testing.T) {
	t.Parallel()

	m := mksmtp.Message{
		From: "hello@acme.com", To: []string{"alice@example.com"},
		BCC: []string{"audit@acme.com"}, Subject: "s", Text: "t", Date: fixedDate(),
	}

	// The envelope carries the blind recipient — that is how they receive it — and the headers
	// do not, which is the whole of what "blind" means. Writing it as a header would disclose
	// the address to every other recipient.
	raw := string(build(t, m))
	if strings.Contains(raw, "audit@acme.com") {
		t.Errorf("a blind recipient appears in the headers:\n%s", raw)
	}

	envelope := m.Recipients()
	if len(envelope) != 2 || envelope[1] != "audit@acme.com" {
		t.Errorf("envelope = %v, want the blind recipient included", envelope)
	}
}

func TestNoMessageIDIsWrittenByDefault(t *testing.T) {
	t.Parallel()

	// The platform assigns one. A second id for one message is worse than none, because the
	// two then disagree in the logs.
	raw := string(build(t, mksmtp.Message{
		From: "hello@acme.com", To: []string{"a@example.com"},
		Subject: "s", Text: "t", Date: fixedDate(),
	}))
	if strings.Contains(raw, "Message-ID") {
		t.Errorf("a Message-ID was written:\n%s", raw)
	}
}

func TestAMessageWithNoDateIsRefusedRatherThanDated(t *testing.T) {
	t.Parallel()

	// Reaching for the wall clock here would make every golden file unpinnable, and would put
	// a clock read inside a package that has no business owning one.
	_, err := mksmtp.Message{
		From: "hello@acme.com", To: []string{"a@example.com"}, Subject: "s", Text: "t",
	}.Build()
	if err == nil {
		t.Fatal("a message with no date was built")
	}
	if !strings.Contains(err.Error(), "clock") {
		t.Errorf("the refusal does not say where a date should come from: %v", err)
	}
}

func TestAMalformedAddressIsRefusedBeforeAnythingIsBuilt(t *testing.T) {
	t.Parallel()

	for _, m := range []mksmtp.Message{
		{From: "not an address", To: []string{"a@example.com"}, Subject: "s", Text: "t", Date: fixedDate()},
		{From: "hello@acme.com", To: []string{"also not one"}, Subject: "s", Text: "t", Date: fixedDate()},
	} {
		if _, err := m.Build(); err == nil {
			t.Errorf("a malformed address was accepted: %+v", m)
		}
	}
}

func TestTheSenderIsTheAddressWithoutItsDisplayName(t *testing.T) {
	t.Parallel()

	// The envelope carries an addr-spec. A display name in MAIL FROM is a syntax error, and
	// one that some servers accept and others do not, which is the worst kind.
	sender, err := mksmtp.Message{From: "Acme <hello@acme.com>"}.Sender()
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	if sender != "hello@acme.com" {
		t.Errorf("sender = %q, want the bare address", sender)
	}
}

func TestLongBase64LinesAreFolded(t *testing.T) {
	t.Parallel()

	// A line over 998 octets is illegal and several relays fold or reject rather than pass it
	// through, so the message that arrives would not be the message that was built.
	raw := string(build(t, mksmtp.Message{
		From: "hello@acme.com", To: []string{"a@example.com"},
		Subject: "s", Text: "t", Date: fixedDate(),
		Attachments: []mksmtp.Attachment{{Filename: "big.bin", Content: make([]byte, 4096)}},
	}))

	for _, line := range strings.Split(raw, "\r\n") {
		if len(line) > 998 {
			t.Fatalf("a line of %d octets was written", len(line))
		}
	}
}

func TestAnASCIISubjectIsLeftUnencoded(t *testing.T) {
	t.Parallel()

	// Encoding unconditionally would turn every plain English subject into an unreadable
	// encoded-word in the tools someone debugging a send actually reaches for.
	raw := string(build(t, mksmtp.Message{
		From: "hello@acme.com", To: []string{"a@example.com"},
		Subject: "Your receipt", Text: "t", Date: fixedDate(),
	}))
	if !strings.Contains(raw, "Subject: Your receipt\r\n") {
		t.Errorf("an ASCII subject was encoded:\n%s", raw)
	}
}
