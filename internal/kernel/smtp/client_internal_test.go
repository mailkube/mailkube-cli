package smtp

import (
	"crypto/tls"
	"strings"
	"testing"
	"time"
)

func TestTheTLSFloorAndHostnameAreNotNegotiable(t *testing.T) {
	t.Parallel()

	// Asserted against the configuration this package builds rather than against a handshake,
	// because a server offering only TLS 1.0 would simply fail, and that failure is
	// indistinguishable from any other. The interesting property is that no code path here can
	// produce a configuration without these two values.
	for _, mode := range []TLSMode{STARTTLS, Implicit} {
		config := Config{Host: "smtp.example.com", Port: 587, TLS: mode}.tls()

		if config.MinVersion != tls.VersionTLS12 {
			t.Errorf("%s: minimum version = 0x%04x, want TLS 1.2", mode, config.MinVersion)
		}
		if config.ServerName != "smtp.example.com" {
			t.Errorf("%s: server name = %q, want the host being connected to", mode, config.ServerName)
		}
		if config.InsecureSkipVerify {
			t.Errorf("%s: verification is disabled", mode)
		}
	}
}

func TestAnEnhancedStatusIsSplitFromTheServersText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reply, enhanced, text string
	}{
		{"5.7.8 Authentication credentials invalid", "5.7.8", "Authentication credentials invalid"},
		{"4.7.0 Too many attempts", "4.7.0", "Too many attempts"},
		{"Mailbox does not exist", "", "Mailbox does not exist"},
		// Not an enhanced status: the class must be 2, 4 or 5, and a version-looking token
		// at the start of a message is otherwise easy to mistake for one.
		{"1.2.3 something", "", "1.2.3 something"},
		{"550 not a status", "", "550 not a status"},
	}

	for _, tc := range tests {
		enhanced, text := splitEnhanced(tc.reply)
		if enhanced != tc.enhanced || text != tc.text {
			t.Errorf("splitEnhanced(%q) = %q/%q, want %q/%q",
				tc.reply, enhanced, text, tc.enhanced, tc.text)
		}
	}
}

func TestAReplyIsRenderedAsABounceQuotesIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  *Error
		want string
	}{
		{&Error{Code: 535, Enhanced: "5.7.8"}, "535 5.7.8"},
		{&Error{Code: 550}, "550"},
		// A failure before any reply has no code to quote, and inventing one would send
		// someone to look up an explanation for something the server never said.
		{&Error{}, ""},
	}

	for _, tc := range tests {
		if got := tc.err.Reply(); got != tc.want {
			t.Errorf("Reply() = %q, want %q", got, tc.want)
		}
	}
}

func TestAnUnfamiliarTLSVersionIsReportedRatherThanHidden(t *testing.T) {
	t.Parallel()

	// A version this release has never heard of still renders, because "the channel is
	// something I do not recognise" is a useful thing to be told and an empty column is not.
	if got := tlsVersionName(0x0304 + 1); got == "" {
		t.Error("an unknown TLS version rendered as nothing")
	}
	if got := tlsVersionName(tls.VersionTLS12); got != "TLS 1.2" {
		t.Errorf("tlsVersionName(TLS 1.2) = %q", got)
	}
}

func TestAMessageIDIsWrittenOnlyWhenADomainIsGiven(t *testing.T) {
	t.Parallel()

	// The send path never asks for one, because the platform assigns it. The capability exists
	// for a caller that has no platform behind it, and it is stable so a golden can pin it.
	m := Message{
		From: "hello@acme.com", To: []string{"a@example.com"}, Subject: "s", Text: "t",
		Date:            fixedInternalDate(),
		MessageIDDomain: "msg.example.com",
	}

	raw, err := m.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(raw), "@msg.example.com>") {
		t.Errorf("no Message-ID was written:\n%s", raw)
	}

	again, err := m.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if string(raw) != string(again) {
		t.Error("the same message produced two different ids")
	}
}

// fixedInternalDate is the date the internal tests build against.
func fixedInternalDate() time.Time { return time.Date(2026, 8, 14, 7, 32, 0, 0, time.UTC) }
