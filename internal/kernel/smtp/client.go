package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// TLSMode is how the connection is encrypted.
type TLSMode string

// The two modes, both of which encrypt. There is deliberately no third.
const (
	// STARTTLS connects in the clear and upgrades. The upgrade is required, never optional:
	// a client that silently continues unencrypted when the server does not offer it is a
	// client that will one day submit a credential in the clear.
	STARTTLS TLSMode = "starttls"
	// Implicit connects inside TLS from the first byte, as port 465 expects.
	Implicit TLSMode = "implicit"
)

// Config is what the client needs to reach a submission server.
type Config struct {
	// Host is the submission host.
	Host string
	// Port is the submission port.
	Port int
	// TLS is how the connection is encrypted.
	TLS TLSMode
	// Username and Password authenticate the session. Both empty means connect without
	// authenticating, which is what the connectivity probe does.
	Username, Password string
	// Timeout bounds the whole conversation.
	Timeout time.Duration

	// tlsConfig is the seam the in-process test server is trusted through, and the only one.
	// There is no flag that weakens verification: a CLI that ships one grows a support answer
	// telling people to use it, and that answer outlives the situation that prompted it.
	tlsConfig *tls.Config
}

// Address is the host and port as a dialable string.
func (c Config) Address() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// WithTLSConfig returns a copy trusting a supplied TLS configuration. For tests.
func (c Config) WithTLSConfig(t *tls.Config) Config {
	c.tlsConfig = t
	return c
}

// Capabilities is what the server said it can do.
type Capabilities struct {
	// StartTLS reports whether the server offers the upgrade.
	StartTLS bool
	// Auth are the advertised mechanisms.
	Auth []string
	// MaxSize is the SIZE the server advertises, or zero when it advertises none.
	//
	// This is the one size limit worth checking locally, because it is exact, current and
	// stated by the server that will enforce it — unlike a per-plan ceiling the CLI would have
	// to guess at.
	MaxSize int64
	// Pipelining and EightBitMIME are reported because `smtp test` shows them.
	Pipelining, EightBitMIME bool
	// TLSVersion and CipherSuite describe the negotiated channel, once there is one.
	TLSVersion, CipherSuite string
	// CertificateSubject and CertificateIssuer describe the verified peer.
	//
	// Verified, not merely presented: the value of showing them is that the TLS stack accepted
	// the chain for this hostname, and an intercepting gateway would fail before this is set.
	CertificateSubject, CertificateIssuer string
	// CertificateExpiry is when the verified leaf expires.
	CertificateExpiry time.Time
}

// Session is one authenticated conversation, reusable for several messages.
//
// It exists because authentication is rate-limited per sending domain, so a client that connected
// and authenticated once per message would throttle itself. Holding one session open for a run is
// both faster and the thing that keeps a bulk send from looking like an attack.
type Session struct {
	client *smtp.Client
	config Config
	caps   Capabilities
}

// Connect opens a session, upgrades it, and authenticates when a credential was supplied.
//
// Context cancellation is honoured on the dial and enforced as a deadline for the rest, which
// net/smtp does not do itself: it takes a net.Conn, so the deadline has to be set on the
// connection before handing it over.
func Connect(ctx context.Context, config Config) (*Session, error) {
	conn, err := dial(ctx, config)
	if err != nil {
		return nil, err
	}

	if config.Timeout > 0 {
		// Set on the connection rather than passed to net/smtp, which has no notion of one.
		// Without it a server that accepts a connection and then says nothing hangs the CLI
		// for as long as the operating system allows.
		if err := conn.SetDeadline(time.Now().Add(config.Timeout)); err != nil { //nolint:forbidigo // a socket deadline, not a rendered time
			return nil, classify(StageDial, err)
		}
	}

	client, err := smtp.NewClient(conn, config.Host)
	if err != nil {
		_ = conn.Close()
		return nil, classify(StageGreeting, err)
	}

	session := &Session{client: client, config: config}
	if err := session.prepare(); err != nil {
		session.Close()
		return nil, err
	}
	return session, nil
}

// dial opens the transport, inside TLS or not depending on the mode.
func dial(ctx context.Context, config Config) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: config.Timeout}

	if config.TLS == Implicit {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: config.tls()}
		conn, err := tlsDialer.DialContext(ctx, "tcp", config.Address())
		if err != nil {
			return nil, classify(StageTLS, err)
		}
		return conn, nil
	}

	conn, err := dialer.DialContext(ctx, "tcp", config.Address())
	if err != nil {
		return nil, classify(StageDial, err)
	}
	return conn, nil
}

// tls returns the TLS configuration, which verifies the hostname in every mode.
func (c Config) tls() *tls.Config {
	if c.tlsConfig != nil {
		return c.tlsConfig
	}
	return &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
}

// prepare reads the greeting's capabilities, upgrades, and authenticates.
func (s *Session) prepare() error {
	s.readCapabilities()

	if s.config.TLS == STARTTLS {
		if !s.caps.StartTLS {
			// Refused, not downgraded. The whole point of asking for STARTTLS is that the
			// alternative is unacceptable, and a fallback would make the flag decorative.
			return &Error{
				Stage:    StageTLS,
				Message:  "the server does not offer STARTTLS, and this client will not submit in the clear",
				category: ErrTLS,
			}
		}
		if err := s.client.StartTLS(s.config.tls()); err != nil {
			return classify(StageTLS, err)
		}
		// The capability list is re-read after the upgrade, because a server may advertise
		// different mechanisms once the channel is encrypted — and usually does, since
		// offering PLAIN in the clear is what a careful server refuses to do.
		s.readCapabilities()
	}
	s.readTLSState()

	if s.config.Username == "" {
		return nil
	}
	return s.authenticate()
}

// authenticate performs PLAIN authentication over the encrypted channel.
//
// net/smtp's PlainAuth refuses to send a credential over an unencrypted connection, which is a
// property worth keeping rather than working around: it is the last check between a misconfigured
// port and a password on the wire.
func (s *Session) authenticate() error {
	auth := smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
	return classify(StageAuth, s.client.Auth(auth))
}

// readCapabilities records what the server advertised.
func (s *Session) readCapabilities() {
	// Extension answers (supported, parameters) in that order.
	//
	// STARTTLS is latched rather than assigned: capabilities are re-read after the upgrade, and
	// an upgraded server no longer advertises the extension it just performed. Assigning here
	// would make a connection that used STARTTLS report that STARTTLS was unavailable, which is
	// the exact opposite of what happened and precisely what `smtp test` exists to tell someone.
	if offered, _ := s.client.Extension("STARTTLS"); offered {
		s.caps.StartTLS = true
	}
	s.caps.Pipelining, _ = s.client.Extension("PIPELINING")
	s.caps.EightBitMIME, _ = s.client.Extension("8BITMIME")

	if ok, params := s.client.Extension("AUTH"); ok {
		s.caps.Auth = strings.Fields(params)
	}
	if ok, params := s.client.Extension("SIZE"); ok {
		if size, err := strconv.ParseInt(strings.TrimSpace(params), 10, 64); err == nil {
			s.caps.MaxSize = size
		}
	}
}

// readTLSState records what the negotiated channel actually is.
func (s *Session) readTLSState() {
	state, ok := s.client.TLSConnectionState()
	if !ok {
		return
	}

	s.caps.TLSVersion = tlsVersionName(state.Version)
	s.caps.CipherSuite = tls.CipherSuiteName(state.CipherSuite)

	if len(state.PeerCertificates) == 0 {
		return
	}
	leaf := state.PeerCertificates[0]
	s.caps.CertificateSubject = leaf.Subject.CommonName
	s.caps.CertificateIssuer = leaf.Issuer.CommonName
	s.caps.CertificateExpiry = leaf.NotAfter
}

// tlsVersionName renders a TLS version the way a person writes it.
func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("0x%04x", version)
	}
}

// Capabilities is what the server advertised and what was negotiated.
func (s *Session) Capabilities() Capabilities { return s.caps }

// Send submits one message.
func (s *Session) Send(message Message) error {
	sender, err := message.Sender()
	if err != nil {
		return &Error{Stage: StageEnvelope, Message: err.Error(), category: ErrPermanent}
	}

	body, err := message.Build()
	if err != nil {
		return &Error{Stage: StageData, Message: err.Error(), category: ErrPermanent}
	}

	if s.caps.MaxSize > 0 && int64(len(body)) > s.caps.MaxSize {
		// Checked before the envelope, because the alternative is uploading the whole
		// message to be told a number the server already told us during the greeting.
		return &Error{
			Stage: StageData,
			Message: fmt.Sprintf("the message is %d bytes and the server accepts at most %d",
				len(body), s.caps.MaxSize),
			category: ErrPermanent,
		}
	}

	if err := classify(StageEnvelope, s.client.Mail(sender)); err != nil {
		return err
	}
	for _, recipient := range message.Recipients() {
		address, err := parseEnvelopeAddress(recipient)
		if err != nil {
			return err
		}
		if err := classify(StageEnvelope, s.client.Rcpt(address)); err != nil {
			return err
		}
	}
	return s.writeData(body)
}

// writeData opens the data phase and writes the message.
func (s *Session) writeData(body []byte) error {
	writer, err := s.client.Data()
	if err != nil {
		return classify(StageData, err)
	}
	if _, err := writer.Write(body); err != nil {
		return classify(StageData, err)
	}
	// The reply to the whole message arrives on close, so this error is the acceptance or the
	// rejection and must never be discarded.
	return classify(StageData, writer.Close())
}

// parseEnvelopeAddress reduces a recipient to the bare address the envelope carries.
func parseEnvelopeAddress(recipient string) (string, error) {
	message := Message{From: recipient}
	address, err := message.Sender()
	if err != nil {
		return "", &Error{Stage: StageEnvelope, Message: err.Error(), category: ErrPermanent}
	}
	return address, nil
}

// Close ends the session politely, falling back to closing the socket.
//
// A QUIT that fails is not worth reporting: the message was already accepted or rejected, and the
// outcome the caller cares about was decided before this.
func (s *Session) Close() {
	if err := s.client.Quit(); err != nil {
		_ = s.client.Close()
	}
}
