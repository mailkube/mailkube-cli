package smtp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// connect starts a fake server and opens a session against it.
func connect(t *testing.T, server *fakeServer, adjust func(*mksmtp.Config)) (*fakeServer, *mksmtp.Session, error) {
	t.Helper()

	server, host, port, trust := newFakeServer(t, server)
	config := mksmtp.Config{
		Host: host, Port: port, TLS: mksmtp.STARTTLS,
		Username: "app01@acme.com", Password: "secret",
		Timeout: 10 * time.Second,
	}
	if adjust != nil {
		adjust(&config)
	}

	session, err := mksmtp.Connect(context.Background(), config.WithTLSConfig(trust))
	if session != nil {
		t.Cleanup(session.Close)
	}
	return server, session, err
}

// message is a valid message for the tests whose subject is the conversation, not the content.
func message() mksmtp.Message {
	return mksmtp.Message{
		From:    "Acme <hello@acme.com>",
		To:      []string{"alice@example.com"},
		Subject: "Welcome",
		Text:    "Thanks for signing up.",
		Date:    time.Date(2026, 8, 14, 7, 32, 0, 0, time.UTC),
	}
}

func TestASessionUpgradesAuthenticatesAndSubmits(t *testing.T) {
	t.Parallel()

	server, session, err := connect(t, &fakeServer{}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	caps := session.Capabilities()
	// The upgrade happened, and the capability survives being re-read afterwards: an upgraded
	// server no longer advertises the extension it just performed, and reporting that as "not
	// offered" would be the exact opposite of what happened.
	if !caps.StartTLS {
		t.Error("a session that used STARTTLS reports that STARTTLS was unavailable")
	}
	if caps.TLSVersion == "" || caps.CipherSuite == "" {
		t.Errorf("the negotiated channel was not recorded: %+v", caps)
	}
	// Verified rather than merely presented: an intercepting gateway fails the handshake
	// before this is ever set, which is the whole value of showing it.
	if caps.CertificateSubject != "localhost" {
		t.Errorf("certificate subject = %q, want the verified peer", caps.CertificateSubject)
	}
	if caps.MaxSize != 20971520 {
		t.Errorf("advertised size = %d, want the value from EHLO", caps.MaxSize)
	}

	if err := session.Send(message()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !server.authenticated() {
		t.Error("the session submitted without authenticating")
	}
	if len(server.messages()) != 1 {
		t.Fatalf("the server received %d messages, want 1", len(server.messages()))
	}
}

func TestAServerWithoutSTARTTLSIsRefusedRatherThanDowngraded(t *testing.T) {
	t.Parallel()

	// The whole point of asking for STARTTLS is that the alternative is unacceptable. A client
	// that quietly continued in the clear would submit the credential in the clear too.
	_, _, err := connect(t, &fakeServer{capabilities: []string{"AUTH PLAIN"}}, nil)
	if err == nil {
		t.Fatal("a server offering no encryption was accepted")
	}
	if !errors.Is(err, mksmtp.ErrTLS) {
		t.Errorf("error = %v, want it to report a TLS failure", err)
	}
	if !strings.Contains(err.Error(), "will not submit in the clear") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func TestAnUntrustedCertificateFailsRatherThanWarning(t *testing.T) {
	t.Parallel()

	// No trust pool, so the self-signed certificate cannot be verified. There is no flag that
	// makes this pass, which is the point.
	server, host, port, _ := newFakeServer(t, &fakeServer{})
	_ = server

	_, err := mksmtp.Connect(context.Background(), mksmtp.Config{
		Host: host, Port: port, TLS: mksmtp.STARTTLS, Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("an unverifiable certificate was accepted")
	}
	if !errors.Is(err, mksmtp.ErrTLS) {
		t.Errorf("error = %v, want a TLS failure", err)
	}
}

func TestARejectedCredentialIsReportedAsAuthentication(t *testing.T) {
	t.Parallel()

	_, _, err := connect(t, &fakeServer{
		replies: map[string]string{"AUTH": "535 5.7.8 Authentication credentials invalid"},
	}, nil)

	if !errors.Is(err, mksmtp.ErrAuth) {
		t.Fatalf("error = %v, want an authentication failure", err)
	}

	var smtpErr *mksmtp.Error
	if !errors.As(err, &smtpErr) {
		t.Fatalf("error = %v, want a *smtp.Error", err)
	}
	if smtpErr.Code != 535 || smtpErr.Enhanced != "5.7.8" {
		t.Errorf("code/enhanced = %d/%q, want 535/5.7.8", smtpErr.Code, smtpErr.Enhanced)
	}
	if smtpErr.Stage != mksmtp.StageAuth {
		t.Errorf("stage = %q, want %q", smtpErr.Stage, mksmtp.StageAuth)
	}
	// The server's own words, kept. A message paraphrased here is one that has to be kept in
	// step with a server nobody in this repository controls.
	if !strings.Contains(smtpErr.Message, "Authentication credentials invalid") {
		t.Errorf("message = %q, want the server's text", smtpErr.Message)
	}
}

func TestAThrottledSignInIsNotReportedAsABadPassword(t *testing.T) {
	t.Parallel()

	// 454 4.7.0 means wait. Reporting it as an authentication failure sends someone to reset a
	// credential that was working, and the reset does not help.
	_, _, err := connect(t, &fakeServer{
		replies: map[string]string{"AUTH": "454 4.7.0 Too many authentication attempts"},
	}, nil)

	if !errors.Is(err, mksmtp.ErrAuthThrottled) {
		t.Fatalf("error = %v, want a throttled-authentication failure", err)
	}
	if errors.Is(err, mksmtp.ErrAuth) {
		t.Error("a throttle was reported as a rejected credential")
	}
}

func TestAFourHundredReplyIsTransientAndAFiveHundredIsNot(t *testing.T) {
	t.Parallel()

	transient, session, err := connect(t, &fakeServer{
		replies: map[string]string{"RCPT": "450 4.2.0 Mailbox temporarily unavailable"},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_ = transient

	err = session.Send(message())
	if !errors.Is(err, mksmtp.ErrTransient) {
		t.Errorf("a 450 is %v, want a transient failure", err)
	}

	_, permanentSession, err := connect(t, &fakeServer{
		replies: map[string]string{"RCPT": "550 5.1.1 Mailbox does not exist"},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := permanentSession.Send(message()); !errors.Is(err, mksmtp.ErrPermanent) {
		t.Errorf("a 550 is %v, want a permanent failure", err)
	}
}

func TestARejectionOnTheFinalDotIsNotLost(t *testing.T) {
	t.Parallel()

	// The reply to the whole message arrives when the data phase closes. A client that
	// discarded that error would report a rejected message as sent, which is the worst
	// possible thing this package could get wrong.
	_, session, err := connect(t, &fakeServer{
		replies: map[string]string{".": "554 5.7.1 Message rejected by policy"},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	err = session.Send(message())
	if err == nil {
		t.Fatal("a message rejected at the final dot was reported as sent")
	}

	var smtpErr *mksmtp.Error
	if errors.As(err, &smtpErr) && smtpErr.Stage != mksmtp.StageData {
		t.Errorf("stage = %q, want %q", smtpErr.Stage, mksmtp.StageData)
	}
}

func TestAMessageLargerThanAdvertisedIsRefusedBeforeUpload(t *testing.T) {
	t.Parallel()

	server, session, err := connect(t, &fakeServer{
		capabilities: []string{"STARTTLS", "AUTH PLAIN", "SIZE 500"},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	big := message()
	big.Text = strings.Repeat("padding ", 500)

	// The server stated the number during the greeting, so uploading the message to be told it
	// again is a round trip that buys nothing — and on a slow link it is a long one.
	if err := session.Send(big); err == nil {
		t.Fatal("an oversized message was uploaded anyway")
	}
	if len(server.messages()) != 0 {
		t.Error("the message reached the server despite the advertised limit")
	}
}

func TestAConnectionWithNoCredentialAuthenticatesNothing(t *testing.T) {
	t.Parallel()

	// This is what the connectivity probe does: it answers "can I reach it and is the channel
	// sound" without putting a credential on the wire.
	server, session, err := connect(t, &fakeServer{}, func(c *mksmtp.Config) {
		c.Username, c.Password = "", ""
	})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if server.authenticated() {
		t.Error("a probe with no credential still authenticated")
	}
	if !session.Capabilities().StartTLS {
		t.Error("the probe did not record the capabilities")
	}
}

func TestImplicitTLSConnectsInsideTLSFromTheFirstByte(t *testing.T) {
	t.Parallel()

	server := &fakeServer{capabilities: []string{"AUTH PLAIN", "PIPELINING"}}
	server, host, port, trust := newFakeServer(t, server)

	// The fake server speaks TLS only after STARTTLS, so this asserts the client's own
	// behaviour: dialling implicitly must not send a plaintext greeting first.
	_, err := mksmtp.Connect(context.Background(), mksmtp.Config{
		Host: host, Port: port, TLS: mksmtp.Implicit, Timeout: 2 * time.Second,
	}.WithTLSConfig(trust))
	if err == nil {
		t.Fatal("an implicit-TLS dial succeeded against a plaintext listener")
	}
	if server.authenticated() {
		t.Error("a credential was sent before the channel was established")
	}
}

func TestACancelledContextStopsTheDial(t *testing.T) {
	t.Parallel()

	_, host, port, trust := newFakeServer(t, &fakeServer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := mksmtp.Connect(ctx, mksmtp.Config{
		Host: host, Port: port, TLS: mksmtp.STARTTLS, Timeout: 5 * time.Second,
	}.WithTLSConfig(trust))
	if err == nil {
		t.Fatal("a cancelled context still opened a session")
	}
}
