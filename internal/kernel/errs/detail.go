package errs

import (
	"errors"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// Detail is everything the CLI knows about a failure, in one shape.
//
// It exists so the text report and the JSON report are two renderings of one value rather than two
// descriptions of one event. A field the JSON carries and the text does not is then a rendering
// choice, not a discrepancy.
type Detail struct {
	// Name is the server's machine-readable error name, passed through unaltered.
	//
	// Empty for a failure that never reached the server. The SDK treats this as an open string,
	// so a name this release has never heard of still arrives here intact.
	Name string `json:"name,omitempty"`
	// Message is the human-readable text. Server-supplied messages are rendered as given.
	Message string `json:"message"`
	// StatusCode is the HTTP status, or zero when there was no response.
	StatusCode int `json:"statusCode,omitempty"`
	// RequestID is the server's request id, to quote when asking for help.
	RequestID string `json:"requestId,omitempty"`
	// RetryAfter is the server's requested wait in seconds, when it sent one.
	RetryAfter int `json:"-"`
	// Retryable says whether re-running the same command could plausibly succeed.
	//
	// It is advice, never an action: nothing in this CLI retries on its own, so this exists to
	// tell the caller which branch to take rather than to trigger one.
	Retryable bool `json:"-"`
	// Code is the exit code this failure produces.
	Code Code `json:"-"`
	// Hints are extra lines to render under the message, supplied by the reporting command.
	Hints []string `json:"-"`
	// RetryNote replaces the default sentence about retrying, when a command has something more
	// specific to say. A send can point at --idempotency-key; a listing has no such advice, and
	// putting that sentence in the shared default would have every command claim it.
	RetryNote string `json:"-"`
}

// Adviser is an error that carries extra lines for its own report.
//
// The interface exists so a command can say something specific about a failure without the kernel
// growing a case for every command in the program. The kernel decides what an error *is*; only
// the command knows what to do about it in that context.
type Adviser interface {
	error
	// Advice returns lines to render under the message, and a retry note replacing the
	// generic one. Either may be empty.
	Advice() (hints []string, retryNote string)
}

// advised attaches reporting advice to an error, leaving it inspectable.
type advised struct {
	err   error
	hints []string
	note  string
}

func (a *advised) Error() string              { return a.err.Error() }
func (a *advised) Unwrap() error              { return a.err }
func (a *advised) Advice() ([]string, string) { return a.hints, a.note }

// Advise attaches reporting lines to an error.
//
// The error keeps its identity: errors.Is and errors.As still see through this, so the exit code
// and the error name are unaffected by a command choosing to explain itself.
func Advise(err error, retryNote string, hints ...string) error {
	if err == nil {
		return nil
	}
	return &advised{err: err, hints: hints, note: retryNote}
}

// envelope is the documented JSON error shape: {"error": {...}}.
type envelope struct {
	Error Detail `json:"error"`
}

// Envelope wraps a Detail in the object shape the JSON contract promises.
func Envelope(d Detail) any { return envelope{Error: d} }

// Describe reduces any error to the one shape the reports are rendered from.
//
// Server-supplied text is carried through untouched. A caller comparing our output against the
// API's own documentation has to see the same words, and a message we paraphrased is one we would
// then have to keep in step with the server forever.
func Describe(err error) Detail {
	if err == nil {
		return Detail{}
	}

	d := Detail{Message: err.Error(), Code: CodeFor(err)}

	var apiErr *mailkube.APIError
	if errors.As(err, &apiErr) {
		d.Name = apiErr.ErrorName
		d.StatusCode = apiErr.StatusCode
		d.RequestID = apiErr.RequestID
		d.RetryAfter = apiErr.RetryAfter
	}

	var submission *smtp.Error
	if errors.As(err, &submission) {
		// The reply code and its enhanced status are the failure's identity on this
		// transport, and they are what `errors explain` is keyed on — so naming them here is
		// what connects a failure a user just saw to the command that explains it.
		d.Name = submission.Reply()
		d.Message = submission.Message
		if reply := submission.Reply(); reply != "" {
			d.Hints = append(d.Hints, "Explain it: mailkube errors explain "+reply)
		}
	}

	var adviser Adviser
	if errors.As(err, &adviser) {
		d.Hints, d.RetryNote = adviser.Advice()
	}

	d.Retryable = retryable(d.Code)
	return d
}

// retryable answers whether re-running the identical command could plausibly succeed.
//
// Only three codes qualify, and the exclusions matter more than the inclusions: authentication is
// never retried, because a rejected credential is not going to be accepted a moment later and
// hammering it is how an address gets blocked.
func retryable(code Code) bool {
	switch code {
	case CodeRateLimit, CodeServer, CodeNetwork:
		return true
	default:
		return false
	}
}
