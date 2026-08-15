package smtp_test

import (
	"bytes"
	"net/mail"
	"strings"
	"testing"
	"time"

	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// FuzzBuild asserts that no field a user controls can forge a header.
//
// This is the security property of the whole SMTP path. Subject, display name, header value and
// filename all arrive as arbitrary text, and a CR or LF that survives into the header section
// ends one header and starts another: an injected Bcc silently copies a stranger, and an injected
// Content-Type rewrites how the body is read. The message either builds with the injected text
// contained, or refuses to build. Those are the only two acceptable outcomes.
//
// The seed corpus runs as an ordinary test on every build, so the known injections stay pinned
// even when the nightly fuzz job is not running.
func FuzzBuild(f *testing.F) {
	seeds := []struct{ subject, name, header, filename string }{
		{"Welcome", "Acme", "launch", "a.txt"},
		{"Hello\r\nBcc: victim@example.com", "Acme", "x", "a.txt"},
		{"Hello\nBcc: victim@example.com", "Acme", "x", "a.txt"},
		{"Hello\rX", "Acme", "x", "a.txt"},
		{"ok", "Acme\r\nBcc: victim@example.com", "x", "a.txt"},
		{"ok", "Acme", "value\r\nContent-Type: text/html", "a.txt"},
		{"ok", "Acme", "x", "report\r\nBcc: victim@example.com.pdf"},
		{"Grüße aus München", "Ärzte Ünion", "wért", "rapport-été.pdf"},
		{"日本語の件名", "山田太郎", "値", "報告書.pdf"},
		{strings.Repeat("long ", 200), "Acme", "x", "a.txt"},
		{"", "", "", ""},
		{"\x00null", "\x00", "\x00", "\x00"},
	}
	for _, s := range seeds {
		f.Add(s.subject, s.name, s.header, s.filename)
	}

	date := time.Date(2026, time.August, 14, 7, 32, 0, 0, time.UTC)

	f.Fuzz(func(t *testing.T, subject, name, header, filename string) {
		sender := mail.Address{Name: name, Address: "hello@acme.com"}

		msg := mksmtp.Message{
			From:        sender.String(),
			To:          []string{"alice@example.com"},
			Subject:     subject,
			Text:        "body",
			Headers:     map[string]string{"X-Campaign": header},
			Attachments: []mksmtp.Attachment{{Filename: filename, Content: []byte("hi")}},
			Date:        date,
		}

		built, err := msg.Build()
		if err != nil {
			return // refusing is a correct outcome; producing a forged header is not
		}

		headers, body, found := bytes.Cut(built, []byte("\r\n\r\n"))
		if !found {
			t.Fatalf("built message has no header/body separator:\n%s", built)
		}
		for _, line := range strings.Split(string(headers), "\r\n") {
			// A continuation line starts with whitespace and belongs to the header above it.
			// Anything else must be a header this package decided to write.
			if line == "" || line[0] == ' ' || line[0] == '\t' {
				continue
			}
			field, _, ok := strings.Cut(line, ":")
			if !ok {
				t.Fatalf("header line is not a header: %q\nin:\n%s", line, headers)
			}
			if !allowed[strings.ToLower(strings.TrimSpace(field))] {
				t.Fatalf("input forged the header %q: %q\nin:\n%s", field, line, headers)
			}
		}
		// The blind copies stay blind, and the message stays parseable by anything that reads
		// mail — including the receiving server, which is the reader that matters.
		if _, err := mail.ReadMessage(bytes.NewReader(built)); err != nil {
			t.Fatalf("built message does not parse as mail: %v\n%s", err, built)
		}
		if len(body) == 0 {
			t.Fatalf("built message has no body:\n%s", built)
		}
	})
}

// allowed is every header this package writes for the message the fuzz target builds. Anything
// outside it in the header section came from the input, which is the failure being hunted.
var allowed = map[string]bool{
	"from": true, "to": true, "subject": true, "reply-to": true, "cc": true,
	"mime-version": true, "content-type": true, "content-transfer-encoding": true,
	"date": true, "x-campaign": true, "x-entity-ref-id": true, "message-id": true,
}
