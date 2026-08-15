package smtp_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer is an in-process submission server.
//
// It exists so the client's own conversation is exercised without a network: the alternative is
// testing against a real server, which makes the suite slow, flaky and dependent on a credential
// nobody has in CI. It speaks only as much of the protocol as this client uses, and answers
// whatever a test tells it to.
type fakeServer struct {
	// capabilities are the EHLO lines after the greeting, without the code prefix.
	capabilities []string
	// replies overrides the answer to a command verb ("AUTH", "MAIL", "RCPT", "DATA", ".").
	replies map[string]string
	// rejectSTARTTLS makes the server refuse to upgrade even though it advertised it.
	rejectSTARTTLS bool

	listener net.Listener
	tlsConf  *tls.Config

	mu       sync.Mutex
	received []string
	authed   bool
}

// newFakeServer starts a server on loopback and returns it with the config to reach it.
func newFakeServer(t *testing.T, server *fakeServer) (*fakeServer, string, int, *tls.Config) {
	t.Helper()

	if server.capabilities == nil {
		server.capabilities = []string{"STARTTLS", "AUTH PLAIN", "PIPELINING", "8BITMIME", "SIZE 20971520"}
	}
	if server.replies == nil {
		server.replies = map[string]string{}
	}

	cert, pool := selfSigned(t)
	server.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	server.listener = listener
	t.Cleanup(func() { _ = listener.Close() })

	go server.serve()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address type %T", listener.Addr())
	}
	// The client verifies the hostname in every mode, so the test trusts the certificate
	// rather than turning verification off: the seam exists for exactly this, and a client with
	// a way to skip verification is a client someone will ship with it skipped.
	return server, "localhost", addr.Port, &tls.Config{ServerName: "localhost", RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// serve accepts connections until the listener closes.
func (s *fakeServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

// handle speaks one conversation.
func (s *fakeServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }

	write("220 fake ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, rest := split(line)

		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			s.writeCapabilities(write)
		case "STARTTLS":
			if s.rejectSTARTTLS {
				write("454 4.7.0 TLS not available")
				continue
			}
			write("220 2.0.0 Ready to start TLS")
			secure := tls.Server(conn, s.tlsConf)
			// This connection outlives the request that started it, and the fake has no
			// deadline of its own to impose, so the handshake is bounded by the socket.
			if err := secure.HandshakeContext(context.Background()); err != nil {
				return
			}
			conn = secure
			reader = bufio.NewReader(conn)
			write = func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
		case "AUTH":
			s.reply(write, "AUTH", "235 2.7.0 Authentication successful")
			s.mu.Lock()
			s.authed = true
			s.mu.Unlock()
		case "MAIL":
			s.reply(write, "MAIL", "250 2.1.0 Ok")
		case "RCPT":
			s.reply(write, "RCPT", "250 2.1.5 Ok")
		case "DATA":
			if reply, overridden := s.replies["DATA"]; overridden {
				write(reply)
				continue
			}
			write("354 End data with <CR><LF>.<CR><LF>")
			s.readMessage(reader)
			s.reply(write, ".", "250 2.0.0 Ok: queued")
		case "QUIT":
			write("221 2.0.0 Bye")
			return
		case "RSET", "NOOP":
			write("250 2.0.0 Ok")
		default:
			write("500 5.5.2 Unrecognized command")
		}
		_ = rest
	}
}

// writeCapabilities answers EHLO with the advertised extensions.
func (s *fakeServer) writeCapabilities(write func(string)) {
	if len(s.capabilities) == 0 {
		write("250 fake")
		return
	}
	write("250-fake")
	for i, capability := range s.capabilities {
		if i == len(s.capabilities)-1 {
			write("250 " + capability)
			continue
		}
		write("250-" + capability)
	}
}

// reply answers with the override for a verb, or with the default.
func (s *fakeServer) reply(write func(string), verb, fallback string) {
	if override, ok := s.replies[verb]; ok {
		write(override)
		return
	}
	write(fallback)
}

// readMessage consumes the data phase and records what arrived.
func (s *fakeServer) readMessage(reader *bufio.Reader) {
	var body strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if strings.TrimRight(line, "\r\n") == "." {
			break
		}
		body.WriteString(line)
	}

	s.mu.Lock()
	s.received = append(s.received, body.String())
	s.mu.Unlock()
}

// messages returns what the server accepted.
func (s *fakeServer) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.received...)
}

// authenticated reports whether a credential was presented.
func (s *fakeServer) authenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authed
}

// split separates a command verb from the rest of its line.
func split(line string) (verb, rest string) {
	verb, rest, _ = strings.Cut(line, " ")
	return verb, rest
}

// selfSigned mints a certificate for localhost and the pool that trusts it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("loading the key pair: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return cert, pool
}
