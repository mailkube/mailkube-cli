package smtp

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"
)

// The two things worth saying about running the same command again.
//
// Both are stated outright, because saying nothing when re-running is worth trying would be the
// same silence as when it is futile, and telling those apart is most of what the reader wants.
const (
	// noteWaitAndRerun is for a fault that may clear without anyone doing anything.
	noteWaitAndRerun = "Nothing is retried automatically. Re-running may succeed once the server answers."
	// noteFixFirst is for a fault that will answer identically until something is changed.
	noteFixFirst = "Re-running will report the same failure until this is fixed."
)

// diagnosis is what this CLI says about a failure the server never gave a reply code for.
//
// A reply-coded failure explains itself: the server sent words, and they pass through unaltered.
// A failure below that level sends no words. What reaches this package instead is a standard
// library string written for whoever is reading a stack trace — "EOF", or an x509 sentence that
// states the same fact three times — and forwarding that as the entire report leaves the reader to
// work out both what happened and whether doing it again could help.
//
// So the entries here exist for exactly the failures that carry no reply: what was observed, in
// one sentence, then what to check, then the truth about retrying. The library's own text is kept
// and rendered underneath, because the person reading this is quite likely the person who will
// need to search for it.
type diagnosis struct {
	// summary is the one sentence that replaces the library's text as the headline.
	summary string
	// actions are the checks and fixes worth trying, in the order worth trying them.
	actions []string
	// retryNote is the truth about running the identical command again.
	retryNote string
}

// Summary is the headline for this failure.
//
// A reply-coded failure is summarised by the server's own words, unaltered: a caller comparing
// this output against the server's documentation has to see the same text. A failure below that
// level has no server text to forward, so the CLI supplies the sentence itself.
func (e *Error) Summary() string {
	if d, ok := e.diagnose(); ok {
		return d.summary
	}
	return e.Message
}

// Advice returns the lines to render under the message and the note about retrying.
//
// This is the reporting-advice interface the error renderer looks for, satisfied structurally so
// that describing a failure stays this package's business and rendering one stays the renderer's.
func (e *Error) Advice() (hints []string, retryNote string) {
	d, ok := e.diagnose()
	if !ok {
		return nil, ""
	}

	hints = append(hints, d.actions...)
	if e.Message != "" && e.Message != d.summary {
		// Kept and marked as such. It is the string someone pastes into a search box, and
		// dropping it in favour of a friendlier sentence would make this report the one
		// place that knows less than the error did.
		hints = append(hints, "Underlying error: "+e.Message)
	}
	return hints, d.retryNote
}

// diagnose describes a failure the server never replied to, or reports that there is nothing to
// add because it did.
//
// The stages that can fail without a reply are the three that establish the session. Everything
// after them is a conversation, and a conversation that goes wrong goes wrong with a reply code.
func (e *Error) diagnose() (diagnosis, bool) {
	if e.Code != 0 {
		return diagnosis{}, false
	}

	switch e.Stage {
	case StageDial:
		return diagnoseDial(e.address, e.cause), true
	case StageGreeting:
		return diagnoseGreeting(e.address, e.cause), true
	case StageTLS:
		return e.diagnoseTLS(), true
	case StageAuth, StageEnvelope, StageData, StageQuit:
		return diagnosis{}, false
	default:
		return diagnosis{}, false
	}
}

// diagnoseDial explains a failure to reach the address at all.
func diagnoseDial(address string, cause error) diagnosis {
	var unresolved *net.DNSError
	if errors.As(cause, &unresolved) {
		return diagnosis{
			summary: fmt.Sprintf("The host %s did not resolve.", unresolved.Name),
			actions: []string{
				"Check: the host is spelled as you meant it — `mailkube config list` shows the one in use.",
				"Fix: pass another with --host, or set it with `mailkube config set smtp_host`.",
			},
			retryNote: noteFixFirst,
		}
	}

	if errors.Is(cause, syscall.ECONNREFUSED) {
		return diagnosis{
			summary: fmt.Sprintf("Nothing accepted a connection on %s.", address),
			actions: []string{
				"Check: the submission service is running and listening on that port.",
				"Check: the port is a submission port — 587 for STARTTLS, 465 for implicit TLS.",
			},
			retryNote: noteFixFirst,
		}
	}

	var timeout net.Error
	if errors.As(cause, &timeout) && timeout.Timeout() {
		return diagnosis{
			summary: fmt.Sprintf("%s did not answer in time.", address),
			actions: []string{
				"Check: a firewall or network policy between here and that port.",
			},
			retryNote: noteWaitAndRerun,
		}
	}

	return diagnosis{
		summary:   fmt.Sprintf("Could not reach %s.", address),
		retryNote: noteWaitAndRerun,
	}
}

// diagnoseGreeting explains a connection that opened and then produced no usable banner.
//
// This is the one failure whose standard-library text is actively misleading by being so short.
// "EOF" reads as a client bug; what happened is that the address is reachable, something accepted
// the connection, and it hung up before saying 220.
func diagnoseGreeting(address string, cause error) diagnosis {
	if errors.Is(cause, io.EOF) || errors.Is(cause, io.ErrUnexpectedEOF) {
		return diagnosis{
			summary: fmt.Sprintf(
				"%s accepted the connection, then closed it before sending a greeting.", address),
			actions: []string{
				"Check: the port is a submission port — 587 for STARTTLS, 465 for implicit TLS.",
				"Fix: if the address is right, whatever answers on it is not serving submission; " +
					"report the address and the time this happened.",
			},
			retryNote: noteWaitAndRerun,
		}
	}

	return diagnosis{
		summary:   fmt.Sprintf("%s did not send a usable greeting.", address),
		retryNote: noteWaitAndRerun,
	}
}

// diagnoseTLS explains a channel that could not be established or could not be trusted.
//
// Verification failures are told apart rather than lumped together, because the four have nothing
// in common but the word "certificate": one is a clock, one is a name, one is a trust store, and
// one is a port speaking the wrong protocol.
func (e *Error) diagnoseTLS() diagnosis {
	if e.cause == nil {
		// The refusal this package raises itself, which is already a sentence.
		return diagnosis{summary: e.Message, retryNote: noteFixFirst}
	}

	var invalid x509.CertificateInvalidError
	if errors.As(e.cause, &invalid) && invalid.Reason == x509.Expired {
		return e.expiredDiagnosis()
	}

	var wrongName x509.HostnameError
	if errors.As(e.cause, &wrongName) {
		return diagnosis{
			summary: fmt.Sprintf("The certificate %s presented is not valid for that name.", e.host),
			actions: []string{
				"Check: " + probeCommand(e.address, e.mode),
				"Fix: connect by a name the certificate covers, or reissue it to cover this one.",
			},
			retryNote: noteFixFirst,
		}
	}

	var untrusted x509.UnknownAuthorityError
	if errors.As(e.cause, &untrusted) {
		return diagnosis{
			summary: fmt.Sprintf(
				"The certificate %s presented was not issued by an authority this machine trusts.", e.host),
			actions: []string{
				"Check: " + probeCommand(e.address, e.mode),
				"Fix: install the issuing authority in this machine's trust store, or serve a " +
					"publicly issued certificate.",
			},
			retryNote: noteFixFirst,
		}
	}

	return e.handshakeDiagnosis()
}

// expiredDiagnosis names the date, which is the whole of what makes an expiry actionable.
//
// The certificate is available even though verification failed, so the report can state when it
// stopped being valid instead of leaving the reader to go and look.
func (e *Error) expiredDiagnosis() diagnosis {
	summary := fmt.Sprintf("The certificate %s presented has expired.", e.host)
	if expiry, ok := expiryOf(e.cause); ok {
		summary = fmt.Sprintf("The certificate %s presented expired on %s.",
			e.host, expiry.UTC().Format(time.DateOnly))
	}

	return diagnosis{
		summary: summary,
		actions: []string{
			"Check: " + probeCommand(e.address, e.mode),
			"Fix: renew the certificate on that server, or report the address if it is not yours.",
		},
		retryNote: noteFixFirst,
	}
}

// handshakeDiagnosis covers a channel that never became TLS at all.
func (e *Error) handshakeDiagnosis() diagnosis {
	var record tls.RecordHeaderError
	if errors.As(e.cause, &record) {
		return diagnosis{
			summary: fmt.Sprintf("%s did not answer with a TLS handshake.", e.address),
			actions: []string{
				"Check: the encryption matches the port — 587 expects starttls, 465 expects implicit.",
				"Fix: pass the other one with --tls.",
			},
			retryNote: noteFixFirst,
		}
	}

	return diagnosis{
		summary:   fmt.Sprintf("The encrypted channel to %s could not be established.", e.address),
		retryNote: noteFixFirst,
	}
}

// expiryOf reports when the certificate the server presented stopped being valid.
func expiryOf(cause error) (time.Time, bool) {
	var verification *tls.CertificateVerificationError
	if !errors.As(cause, &verification) || len(verification.UnverifiedCertificates) == 0 {
		return time.Time{}, false
	}
	return verification.UnverifiedCertificates[0].NotAfter, true
}

// probeCommand is the one-liner that shows the certificate a failure is about.
//
// It is spelled for the mode that was asked for, because the two forms differ and a suggestion
// that does not run is worse than none.
func probeCommand(address string, mode TLSMode) string {
	if mode == Implicit {
		return "openssl s_client -connect " + address
	}
	return "openssl s_client -connect " + address + " -starttls smtp"
}
