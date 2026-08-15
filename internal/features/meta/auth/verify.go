package auth

import (
	"context"
	"errors"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
)

// probeAddress is the from address the platform is guaranteed to reject.
//
// .invalid is reserved by RFC 2606 and can never be registered, so no key can ever be bound to
// this domain and the check can never accidentally send anything. It is also why the address is
// a constant rather than something derived from the user's own domain: a probe that could
// succeed is not a probe.
const probeAddress = "verify@mailkube-cli.invalid"

// probeSubject and probeText describe the request in case it is ever seen in a log.
const (
	probeSubject = "mailkube CLI credential check"
	probeText    = "This request is a credential check and is expected to be rejected."
)

// Verification is what the probe learned about a credential.
type Verification struct {
	// Verified reports whether the key authenticated.
	Verified bool `json:"verified"`
	// Message is the server's own explanation, rendered exactly as it arrived.
	//
	// It carries the domain the key is bound to, which is the second thing this probe answers.
	// The CLI does not parse it: scraping prose for a value breaks on the first wording change,
	// and the server's message is passed through unaltered everywhere else too.
	Message string `json:"message,omitempty"`
	// RequestID is the server's request id for the probe.
	RequestID string `json:"requestId,omitempty"`
}

// verifyKey establishes whether a key authenticates, without sending anything.
//
// The mechanism is a send the server is guaranteed to reject on its very first check: the from
// domain cannot belong to any key. That rejection happens before the quota charge, before the
// body scan and before submission, so the probe costs one slot against the send rate limit and
// nothing else. There is no key-introspection endpoint to ask instead, and a check that told the
// user their key was fine without ever authenticating it would be worse than no check.
//
// Three outcomes, and the middle one is the interesting one:
//
//   - the key is rejected, and that is an authentication failure the caller must not store past;
//   - the from domain is rejected, which means the key authenticated: verified;
//   - anything else, including an unexpected success, is inconclusive rather than a failure.
//     Reporting inconclusive as broken would refuse to store a working credential because the
//     platform answered in a way this release has not seen.
func verifyKey(ctx context.Context, sender ports.EmailSender) (Verification, error) {
	_, err := sender.Send(ctx, mailkube.SendEmailParams{
		From:    probeAddress,
		To:      []string{probeAddress},
		Subject: probeSubject,
		Text:    probeText,
	})

	var apiErr *mailkube.APIError
	if !errors.As(err, &apiErr) {
		return Verification{}, nil
	}

	switch apiErr.ErrorName {
	case mailkube.ErrorNameFromDomainNotAllowed:
		return Verification{Verified: true, Message: apiErr.Message, RequestID: apiErr.RequestID}, nil
	case mailkube.ErrorNameInvalidAPIKey:
		return Verification{}, errs.WithCode(errs.CodeAuth, err)
	default:
		return Verification{RequestID: apiErr.RequestID}, nil
	}
}
