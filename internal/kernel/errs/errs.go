// Package errs turns an error into the process exit code and the report the user reads.
//
// Everything above this package returns an error. This is the single place that decides what an
// error means, so the mapping is one chain a reader can check against the documented table rather
// than a decision scattered across every command.
package errs

import (
	"context"
	"errors"
	"fmt"

	mailkube "github.com/mailkube/mailkube-go"
)

// Code is a process exit code.
//
// The set is part of the CLI's public contract: scripts branch on it, so a code's meaning may not
// change within a major version, and a new one may only ever be added.
type Code int

// The exit codes. Anything not listed here is not produced.
const (
	// CodeOK means the command did what was asked.
	CodeOK Code = 0
	// CodeInternal is a defect in this program: an unhandled panic or an impossible state.
	// It is deliberately distinct from every code below, all of which are expected outcomes.
	CodeInternal Code = 1
	// CodeUsage means the command line itself was wrong, before anything was attempted.
	CodeUsage Code = 2
	// CodeAuth means a credential was missing, malformed or rejected.
	CodeAuth Code = 3
	// CodeValidation means the request was well-formed as a command but invalid as a request.
	CodeValidation Code = 4
	// CodeConfig means a precondition was not met: configuration, entitlement or state.
	CodeConfig Code = 5
	// CodeNotFound means a referenced resource does not exist.
	CodeNotFound Code = 6
	// CodeRateLimit means the server asked us to slow down.
	CodeRateLimit Code = 7
	// CodeServer means the server failed, or answered in a way no client can act on.
	CodeServer Code = 8
	// CodeNetwork means the server was never reached.
	CodeNetwork Code = 9
	// CodePartial is reserved for a future bulk verb, where some items succeed and some fail.
	//
	// It is defined but never returned. Reserving it now is what lets a bulk send be added later
	// without changing the meaning of any code a script already branches on.
	CodePartial Code = 10
	// CodeDeadline means a deadline the caller set was reached with no result.
	//
	// This covers --timeout and --exit-timeout alike, which is why it is not folded into
	// CodeNetwork: "the time you allowed ran out" and "the network failed" call for different
	// responses from a script, and only the first is a decision the caller made.
	CodeDeadline Code = 124
	// CodeInterrupt means the process was asked to stop, by a signal or a cancelled context.
	CodeInterrupt Code = 130
)

// ExitCoder is an error that knows its own exit code.
//
// The interface is exported so a feature can define a domain error carrying its meaning, rather
// than CodeFor growing a case for every feature in the program.
type ExitCoder interface {
	error
	ExitCode() Code
}

// coded attaches an exit code to an error without hiding it from errors.Is and errors.As.
type coded struct {
	code Code
	err  error
}

// Unwrap is what keeps the wrapped error inspectable: errors.Is and errors.As walk through this,
// so tagging an error with a code never costs the caller the ability to ask what it was.
func (c *coded) Error() string  { return c.err.Error() }
func (c *coded) Unwrap() error  { return c.err }
func (c *coded) ExitCode() Code { return c.code }

// WithCode tags err with an exit code, leaving it fully inspectable by errors.Is and errors.As.
//
// Returns nil for a nil error, so it can wrap a call's result without a guard at every call site.
func WithCode(code Code, err error) error {
	if err == nil {
		return nil
	}
	return &coded{code: code, err: err}
}

// Newf builds a new error carrying an exit code.
func Newf(code Code, format string, a ...any) error {
	return &coded{code: code, err: fmt.Errorf(format, a...)}
}

// Usagef reports a command line this program cannot act on: a bad flag combination, a missing
// argument, or a verb that does not exist. Nothing was attempted.
func Usagef(format string, a ...any) error { return Newf(CodeUsage, format, a...) }

// Validationf reports input this program rejected before sending it anywhere.
func Validationf(format string, a ...any) error { return Newf(CodeValidation, format, a...) }

// Configf reports an unmet precondition: no credential, an unreadable config, a port in use.
func Configf(format string, a ...any) error { return Newf(CodeConfig, format, a...) }

// CodeFor decides what an error means to the caller's shell.
//
// The order below is the whole design, and each step is there because the one after it is too
// coarse to answer correctly on its own:
//
//  1. An explicit code wins. If something in this program already decided what an error means,
//     that decision beats anything inferred from its contents.
//  2. Cancellation and deadlines come next, because the SDK reports an expired context as a
//     connection failure. Read in the other order, a --timeout that ran out would be
//     indistinguishable from a network that failed, and only one of those is the caller's doing.
//  3. The error name beats the status. HTTP 403 covers three different situations, only one of
//     which is a credential problem, so reporting "auth" for a plan entitlement sends a human to
//     re-check a key that was fine.
//  4. The category sentinel is the general case.
//  5. Two defaults, because the SDK constructs errors matching no category at all. Both are
//     reachable from ordinary input, and an error that fell through every case still has to exit
//     as something a script can read.
func CodeFor(err error) Code {
	if err == nil {
		return CodeOK
	}

	var coder ExitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}

	switch {
	case errors.Is(err, context.Canceled):
		return CodeInterrupt
	case errors.Is(err, context.DeadlineExceeded):
		return CodeDeadline
	}

	var apiErr *mailkube.APIError
	if errors.As(err, &apiErr) {
		if code, ok := codeForName(apiErr.ErrorName); ok {
			return code
		}
	}

	return codeForSentinel(err)
}

// codeForName handles the error names whose category the HTTP status alone gets wrong.
//
// Both entries below are 403s that are not authentication failures: one is a plan entitlement and
// one is a client-configuration precondition. Every other name is served correctly by its status,
// and listing them here would be a second mapping to keep in step with the first.
func codeForName(name string) (Code, bool) {
	switch name {
	case mailkube.ErrorNameSchedulingNotIncluded, mailkube.ErrorNameBrowserNotAllowed:
		return CodeConfig, true
	default:
		return 0, false
	}
}

// codeForSentinel maps the SDK's category sentinels.
//
// Order is load-bearing twice over. Every sentinel wraps ErrMailkube, so the base must be last or
// it would swallow all of them. And *APIError reports itself as ErrAPI unconditionally, so ErrAPI
// must come after the specific categories or every API error would land on the server row.
func codeForSentinel(err error) Code {
	// Written as an ordered list rather than a switch so the order is visibly the point. The
	// two catch-alls sit at the bottom for the same reason they are last in the chain.
	table := []struct {
		sentinel error
		code     Code
	}{
		{mailkube.ErrNoAPIKey, CodeAuth},
		{mailkube.ErrAuthentication, CodeAuth},
		{mailkube.ErrBadRequest, CodeValidation},
		{mailkube.ErrInvalidRequest, CodeValidation},
		{mailkube.ErrSignatureVerification, CodeValidation},
		{mailkube.ErrConflict, CodeConfig},
		{mailkube.ErrNotFound, CodeNotFound},
		{mailkube.ErrRateLimit, CodeRateLimit},
		{mailkube.ErrServer, CodeServer},
		{mailkube.ErrUnexpectedResponse, CodeServer},
		{mailkube.ErrConnection, CodeNetwork},
		{mailkube.ErrAPI, CodeServer},
		{mailkube.ErrMailkube, CodeConfig},
	}

	for _, row := range table {
		if errors.Is(err, row.sentinel) {
			return row.code
		}
	}
	// Nothing reaches the caller unmapped. An error this release has never seen still exits as
	// something a script can branch on.
	return CodeServer
}
