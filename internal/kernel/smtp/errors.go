package smtp

import (
	"errors"
	"fmt"
	"net/textproto"
	"strconv"
	"strings"
)

// The categories a submission failure falls into.
//
// They exist so a caller can branch without parsing a reply code, and the split is the one that
// changes what a caller should do: wait and retry, fix the message, or fix the credential. A
// fourth category for "authentication is being throttled" is separate from plain authentication
// failure because the two look identical to a user and call for opposite responses — one means
// wait, the other means stop and check.
var (
	// ErrSMTP is the base every failure in this package wraps.
	ErrSMTP = errors.New("smtp")
	// ErrTransient is a failure that may succeed if repeated: a 4xx reply.
	ErrTransient = fmt.Errorf("%w: temporary failure", ErrSMTP)
	// ErrPermanent is a failure that will not: a 5xx reply.
	ErrPermanent = fmt.Errorf("%w: permanent failure", ErrSMTP)
	// ErrAuth is a rejected credential.
	ErrAuth = fmt.Errorf("%w: authentication failed", ErrSMTP)
	// ErrAuthThrottled is authentication refused for arriving too often.
	ErrAuthThrottled = fmt.Errorf("%w: authentication throttled", ErrSMTP)
	// ErrTLS is a failure to establish or verify the encrypted channel.
	ErrTLS = fmt.Errorf("%w: tls failed", ErrSMTP)
	// ErrConnection is a failure to reach the server at all.
	ErrConnection = fmt.Errorf("%w: connection failed", ErrSMTP)
)

// Stage names the point in the conversation a failure happened at.
//
// It is the difference between "your credential is wrong" and "your message was refused", which
// the reply code alone does not always make obvious.
type Stage string

// The stages, in the order a submission passes through them.
const (
	StageDial     Stage = "dial"
	StageGreeting Stage = "greeting"
	StageTLS      Stage = "starttls"
	StageAuth     Stage = "auth"
	StageEnvelope Stage = "envelope"
	StageData     Stage = "data"
	StageQuit     Stage = "quit"
)

// Error is a failure carrying everything the reply said.
//
// It keeps the server's own text rather than substituting a friendlier sentence: the caller
// renders the explanation, and a message paraphrased here is one that has to be kept in step with
// a server nobody here controls.
type Error struct {
	// Code is the three-digit reply code, or zero when the failure happened before a reply.
	Code int
	// Enhanced is the RFC 3463 enhanced status, when the reply carried one.
	Enhanced string
	// Stage is where in the conversation this happened.
	Stage Stage
	// Message is the server's own text, or the local error when there was no reply.
	Message string
	// category is the sentinel this failure reports itself as.
	category error
}

// Error implements error.
func (e *Error) Error() string {
	parts := []string{string(e.Stage)}
	if e.Code != 0 {
		parts = append(parts, fmt.Sprintf("%d", e.Code))
	}
	if e.Enhanced != "" {
		parts = append(parts, e.Enhanced)
	}
	return strings.Join(parts, " ") + ": " + e.Message
}

// Reply is the code with its enhanced status, which is how a bounce quotes a failure and how the
// explanation catalogue is keyed.
func (e *Error) Reply() string {
	if e.Code == 0 {
		return ""
	}
	if e.Enhanced == "" {
		return strconv.Itoa(e.Code)
	}
	return strconv.Itoa(e.Code) + " " + e.Enhanced
}

// Unwrap reports the category, so errors.Is finds both it and ErrSMTP beneath it.
func (e *Error) Unwrap() error { return e.category }

// Is lets a caller match on a bare category as well as on a constructed Error.
func (e *Error) Is(target error) bool { return errors.Is(e.category, target) }

// classify turns whatever net/smtp returned into an Error of this package's shape.
//
// The reply code is the primary signal and the enhanced status refines it. 4xx is transient and
// 5xx permanent by definition; the two authentication cases are pulled out because they are the
// ones a user acts on differently, and 454 4.7.0 in particular means "wait", not "your password is
// wrong" — advice that would send someone to reset a credential that was fine.
func classify(stage Stage, err error) error {
	if err == nil {
		return nil
	}

	var reply *textproto.Error
	if !errors.As(err, &reply) {
		return &Error{Stage: stage, Message: err.Error(), category: categoryForStage(stage)}
	}

	enhanced, text := splitEnhanced(reply.Msg)
	return &Error{
		Code:     reply.Code,
		Enhanced: enhanced,
		Stage:    stage,
		Message:  text,
		category: categoryForReply(stage, reply.Code, enhanced),
	}
}

// categoryForStage is the category when the failure carried no reply code at all.
func categoryForStage(stage Stage) error {
	switch stage {
	case StageDial:
		return ErrConnection
	case StageTLS:
		return ErrTLS
	case StageGreeting, StageAuth, StageEnvelope, StageData, StageQuit:
		return ErrTransient
	default:
		return ErrTransient
	}
}

// categoryForReply maps a reply code and enhanced status onto a category.
func categoryForReply(stage Stage, code int, enhanced string) error {
	if stage == StageAuth {
		// 454 4.7.0 is the throttle: temporary, and the one authentication failure where
		// waiting is the right response rather than checking the credential.
		if code/100 == 4 {
			return ErrAuthThrottled
		}
		return ErrAuth
	}

	switch {
	case code/100 == 4:
		return ErrTransient
	case code/100 == 5 && strings.HasPrefix(enhanced, "5.7"):
		// A policy rejection. Still permanent, and named separately in the catalogue the
		// caller renders from.
		return ErrPermanent
	case code/100 == 5:
		return ErrPermanent
	default:
		return ErrTransient
	}
}

// splitEnhanced separates a leading RFC 3463 status from the human text of a reply.
//
// A server that sends one puts it first, and a caller wants them apart: the status is what a
// catalogue is keyed on, and the text is what a person reads.
func splitEnhanced(msg string) (enhanced, text string) {
	first, rest, found := strings.Cut(strings.TrimSpace(msg), " ")
	if !found || !looksEnhanced(first) {
		return "", strings.TrimSpace(msg)
	}
	return first, strings.TrimSpace(rest)
}

// looksEnhanced reports whether a token is an RFC 3463 status: class.subject.detail, all digits,
// with a leading class of 2, 4 or 5.
func looksEnhanced(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for i := range len(part) {
			if part[i] < '0' || part[i] > '9' {
				return false
			}
		}
	}
	return parts[0] == "2" || parts[0] == "4" || parts[0] == "5"
}
