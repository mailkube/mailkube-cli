package smtp

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"net"
	"net/textproto"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// expiredVerification is what crypto/tls hands back when the chain is sound and the clock is not.
//
// Built rather than provoked from a real handshake: the property under test is that the report
// names the date, and generating an expired certificate to prove that would test the generator.
func expiredVerification(notAfter time.Time) error {
	leaf := &x509.Certificate{NotAfter: notAfter}
	return &tls.CertificateVerificationError{
		UnverifiedCertificates: []*x509.Certificate{leaf},
		Err:                    x509.CertificateInvalidError{Cert: leaf, Reason: x509.Expired},
	}
}

// located is a failure as Connect would return it, address and all.
func located(stage Stage, cause error) *Error {
	err := classify(stage, cause)
	config := Config{Host: "smtp.example.com", Port: 587, TLS: STARTTLS}
	_ = config.locate(err)

	var failure *Error
	if !errors.As(err, &failure) {
		panic("classify did not produce an *Error")
	}
	return failure
}

func TestAFailureWithNoReplyIsExplainedRatherThanForwarded(t *testing.T) {
	t.Parallel()

	// The library's own text is on the left. None of it is a sentence a user can act on, and
	// two of them read as a bug in this program rather than a condition of the server.
	tests := []struct {
		name    string
		stage   Stage
		cause   error
		summary string
		note    string
	}{
		{
			name:    "a connection accepted and dropped before the banner",
			stage:   StageGreeting,
			cause:   io.EOF,
			summary: "smtp.example.com:587 accepted the connection, then closed it before sending a greeting.",
			note:    noteWaitAndRerun,
		},
		{
			name:    "nothing listening",
			stage:   StageDial,
			cause:   &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
			summary: "Nothing accepted a connection on smtp.example.com:587.",
			note:    noteFixFirst,
		},
		{
			name:    "a name that does not resolve",
			stage:   StageDial,
			cause:   &net.DNSError{Name: "smtp.example.com", IsNotFound: true},
			summary: "The host smtp.example.com did not resolve.",
			note:    noteFixFirst,
		},
		{
			name:    "a dial that timed out",
			stage:   StageDial,
			cause:   os.ErrDeadlineExceeded,
			summary: "smtp.example.com:587 did not answer in time.",
			note:    noteWaitAndRerun,
		},
		{
			name:    "an expired certificate",
			stage:   StageTLS,
			cause:   expiredVerification(time.Date(2026, 9, 2, 9, 53, 23, 0, time.UTC)),
			summary: "The certificate smtp.example.com presented expired on 2026-09-02.",
			note:    noteFixFirst,
		},
		{
			name:  "a certificate for another name",
			stage: StageTLS,
			// The certificate is not optional here: rendering this error reads it.
			cause: x509.HostnameError{
				Host: "smtp.example.com",
				Certificate: &x509.Certificate{
					Subject: pkix.Name{CommonName: "smtp.elsewhere.example"},
				},
			},
			summary: "The certificate smtp.example.com presented is not valid for that name.",
			note:    noteFixFirst,
		},
		{
			name:  "a certificate from an authority this machine does not trust",
			stage: StageTLS,
			cause: x509.UnknownAuthorityError{},
			summary: "The certificate smtp.example.com presented was not issued by an authority " +
				"this machine trusts.",
			note: noteFixFirst,
		},
		{
			name:    "a port that does not speak TLS",
			stage:   StageTLS,
			cause:   tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"},
			summary: "smtp.example.com:587 did not answer with a TLS handshake.",
			note:    noteFixFirst,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			failure := located(tc.stage, tc.cause)
			if got := failure.Summary(); got != tc.summary {
				t.Errorf("summary = %q, want %q", got, tc.summary)
			}

			hints, note := failure.Advice()
			if note != tc.note {
				t.Errorf("retry note = %q, want %q", note, tc.note)
			}
			// The library's text survives as the last line. It is what someone searches for,
			// and a report that replaced it would be the only place that knows less than the
			// error did.
			if last := hints[len(hints)-1]; !strings.HasPrefix(last, "Underlying error: ") {
				t.Errorf("last hint = %q, want the underlying error", last)
			}
			if !strings.Contains(strings.Join(hints, "\n"), tc.cause.Error()) {
				t.Errorf("hints %q do not carry the underlying error %q", hints, tc.cause)
			}
		})
	}
}

func TestAnExpiryWithNoCertificateToReadStillNamesTheProblem(t *testing.T) {
	t.Parallel()

	// A verification error can arrive without the chain that produced it. The date is then
	// unknown, and saying so beats printing a zero one.
	failure := located(StageTLS, x509.CertificateInvalidError{Reason: x509.Expired})

	want := "The certificate smtp.example.com presented has expired."
	if got := failure.Summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestAReplyCodedFailureKeepsTheServersOwnWords(t *testing.T) {
	t.Parallel()

	// The rule this protects: text the server supplied is rendered as given. A summary written
	// here would be one that has to be kept in step with a server nobody in this repo controls.
	err := classify(StageAuth, &textproto.Error{
		Code: 535, Msg: "5.7.8 Authentication credentials invalid",
	})

	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("classify returned %T, want *Error", err)
	}

	if got := failure.Summary(); got != "Authentication credentials invalid" {
		t.Errorf("summary = %q, want the server's text unaltered", got)
	}
	hints, note := failure.Advice()
	if hints != nil || note != "" {
		t.Errorf("Advice() = %q/%q, want nothing: the reply speaks for itself", hints, note)
	}
}

func TestTheRefusalToSubmitInTheClearIsAlreadyASentence(t *testing.T) {
	t.Parallel()

	// This one is raised by this package rather than by a library, so there is nothing to
	// improve on — but the retry note still has to be right, and it is not "try again".
	failure := &Error{
		Stage:    StageTLS,
		Message:  "the server does not offer STARTTLS, and this client will not submit in the clear",
		category: ErrTLS,
	}

	if got := failure.Summary(); got != failure.Message {
		t.Errorf("summary = %q, want the message unchanged", got)
	}
	if _, note := failure.Advice(); note != noteFixFirst {
		t.Errorf("retry note = %q, want %q", note, noteFixFirst)
	}
}

func TestAnUnrecognisedCauseStillSaysWhereItHappened(t *testing.T) {
	t.Parallel()

	tests := []struct {
		stage   Stage
		summary string
	}{
		{StageDial, "Could not reach smtp.example.com:587."},
		{StageGreeting, "smtp.example.com:587 did not send a usable greeting."},
		{StageTLS, "The encrypted channel to smtp.example.com:587 could not be established."},
	}

	for _, tc := range tests {
		failure := located(tc.stage, errors.New("something this release has never seen"))
		if got := failure.Summary(); got != tc.summary {
			t.Errorf("%s: summary = %q, want %q", tc.stage, got, tc.summary)
		}
	}
}

func TestTheSuggestedProbeIsSpelledForSTARTTLS(t *testing.T) {
	t.Parallel()

	// A command that does not run is worse than no command; the probe must carry the
	// -starttls flag the submission port expects.
	starttls := located(StageTLS, x509.UnknownAuthorityError{})
	if hints, _ := starttls.Advice(); !strings.HasSuffix(hints[0], "-starttls smtp") {
		t.Errorf("starttls probe = %q, want -starttls smtp", hints[0])
	}
}

func TestTheAddressIsStampedOnceAndNotOverwritten(t *testing.T) {
	t.Parallel()

	// locate runs on the way out of Connect. A failure that already names an address has been
	// through it, and a second pass must not relabel it.
	err := classify(StageDial, io.EOF)
	first := Config{Host: "first.example.com", Port: 587, TLS: STARTTLS}
	second := Config{Host: "second.example.com", Port: 2587, TLS: STARTTLS}

	_ = second.locate(first.locate(err))

	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("classify returned %T, want *Error", err)
	}
	if failure.address != "first.example.com:587" {
		t.Errorf("address = %q, want the first one stamped", failure.address)
	}
}

func TestAFailureDuringTheConversationIsLeftAlone(t *testing.T) {
	t.Parallel()

	// Only the three stages that establish a session can fail without a reply. A socket that
	// dies mid-conversation is rare enough, and ambiguous enough, that inventing a sentence for
	// it would be guessing — so the library's text stands and no advice is offered.
	for _, stage := range []Stage{StageAuth, StageEnvelope, StageData, StageQuit, Stage("unknown")} {
		failure := located(stage, io.ErrUnexpectedEOF)

		if got := failure.Summary(); got != io.ErrUnexpectedEOF.Error() {
			t.Errorf("%s: summary = %q, want the underlying text", stage, got)
		}
		if hints, note := failure.Advice(); hints != nil || note != "" {
			t.Errorf("%s: Advice() = %q/%q, want nothing", stage, hints, note)
		}
	}
}

func TestLocatingSomethingThatIsNotASubmissionFailureChangesNothing(t *testing.T) {
	t.Parallel()

	plain := errors.New("not from this package")
	if got := (Config{Host: "smtp.example.com", Port: 587}).locate(plain); !errors.Is(got, plain) {
		t.Errorf("locate() = %v, want the error it was given", got)
	}
}
