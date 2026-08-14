package errs_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

func TestCodeForMapsEverySentinel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want errs.Code
	}{
		{"nil is success", nil, errs.CodeOK},
		{"no api key", mailkube.ErrNoAPIKey, errs.CodeAuth},
		{"authentication", mailkube.ErrAuthentication, errs.CodeAuth},
		{"bad request", mailkube.ErrBadRequest, errs.CodeValidation},
		{"invalid request", mailkube.ErrInvalidRequest, errs.CodeValidation},
		{"signature verification", mailkube.ErrSignatureVerification, errs.CodeValidation},
		{"conflict", mailkube.ErrConflict, errs.CodeConfig},
		{"not found", mailkube.ErrNotFound, errs.CodeNotFound},
		{"rate limit", mailkube.ErrRateLimit, errs.CodeRateLimit},
		{"server", mailkube.ErrServer, errs.CodeServer},
		{"unexpected response", mailkube.ErrUnexpectedResponse, errs.CodeServer},
		{"connection", mailkube.ErrConnection, errs.CodeNetwork},
		{"uncategorised api error", mailkube.ErrAPI, errs.CodeServer},
		{"base sdk error", mailkube.ErrMailkube, errs.CodeConfig},
		{"a stranger", errors.New("something else entirely"), errs.CodeServer},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errs.CodeFor(tc.err); got != tc.want {
				t.Errorf("CodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// The SDK's sentinels all wrap ErrMailkube, and *APIError reports itself as ErrAPI. Both facts
// make the chain order load-bearing, so assert the specific category wins over the general one
// rather than trusting the source to stay in the right order.
func TestCodeForPrefersTheSpecificCategory(t *testing.T) {
	t.Parallel()

	if !errors.Is(mailkube.ErrRateLimit, mailkube.ErrMailkube) {
		t.Fatal("premise changed: sentinels no longer wrap ErrMailkube")
	}
	if got := errs.CodeFor(mailkube.ErrRateLimit); got != errs.CodeRateLimit {
		t.Errorf("rate limit fell through to the base error: got %d", got)
	}

	apiErr := &mailkube.APIError{ErrorName: "quota_exceeded", StatusCode: 422}
	if !errors.Is(apiErr, mailkube.ErrAPI) {
		t.Fatal("premise changed: *APIError no longer reports itself as ErrAPI")
	}
	if got := errs.CodeFor(apiErr); got != errs.CodeValidation {
		t.Errorf("APIError fell through to the ErrAPI row: got %d, want %d", got, errs.CodeValidation)
	}
}

// HTTP 403 covers three situations and only one of them is a credential problem. Reporting "auth"
// for a plan entitlement sends a script down the wrong branch, so the name has to beat the status.
func TestCodeForPrefersTheNameOverTheStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want errs.Code
	}{
		{mailkube.ErrorNameInvalidAPIKey, errs.CodeAuth},
		{mailkube.ErrorNameSchedulingNotIncluded, errs.CodeConfig},
		{mailkube.ErrorNameBrowserNotAllowed, errs.CodeConfig},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := &mailkube.APIError{ErrorName: tc.name, StatusCode: 403}
			if got := errs.CodeFor(err); got != tc.want {
				t.Errorf("CodeFor(403 %s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// A deadline the caller set and a network that failed are different outcomes, and the SDK reports
// the first as the second. Read in the wrong order, --timeout would be indistinguishable from an
// unreachable server.
func TestCodeForSeparatesDeadlineFromNetworkFailure(t *testing.T) {
	t.Parallel()

	deadline := fmt.Errorf("%w: %w", mailkube.ErrConnection, context.DeadlineExceeded)
	if got := errs.CodeFor(deadline); got != errs.CodeDeadline {
		t.Errorf("expired deadline = %d, want %d", got, errs.CodeDeadline)
	}
	if got := errs.CodeFor(mailkube.ErrConnection); got != errs.CodeNetwork {
		t.Errorf("connection failure = %d, want %d", got, errs.CodeNetwork)
	}
	if got := errs.CodeFor(context.Canceled); got != errs.CodeInterrupt {
		t.Errorf("cancelled context = %d, want %d", got, errs.CodeInterrupt)
	}
}

func TestCodeForHonoursAnExplicitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want errs.Code
	}{
		{"usage", errs.Usagef("--all and --page are mutually exclusive"), errs.CodeUsage},
		{"validation", errs.Validationf("invalid tag name %q", "a b"), errs.CodeValidation},
		{"config", errs.Configf("no SMTP credential in profile %q", "default"), errs.CodeConfig},
		{"tagged", errs.WithCode(errs.CodeNotFound, errors.New("no such topic")), errs.CodeNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errs.CodeFor(tc.err); got != tc.want {
				t.Errorf("CodeFor(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// An explicit decision has to beat an inferred one, including when it is buried under wrapping and
// including when the wrapped error would otherwise map somewhere else.
func TestExplicitCodeSurvivesWrappingAndOutranksInference(t *testing.T) {
	t.Parallel()

	tagged := errs.WithCode(errs.CodeUsage, mailkube.ErrRateLimit)
	if got := errs.CodeFor(fmt.Errorf("building the request: %w", tagged)); got != errs.CodeUsage {
		t.Errorf("wrapped explicit code = %d, want %d", got, errs.CodeUsage)
	}

	// Tagging must not cost the caller the ability to ask what actually happened.
	if !errors.Is(tagged, mailkube.ErrRateLimit) {
		t.Error("WithCode hid the wrapped error from errors.Is")
	}
	var apiErr *mailkube.APIError
	if errors.As(errs.WithCode(errs.CodeUsage, &mailkube.APIError{StatusCode: 429}), &apiErr) {
		if apiErr.StatusCode != 429 {
			t.Errorf("errors.As recovered the wrong value: %d", apiErr.StatusCode)
		}
	} else {
		t.Error("WithCode hid the wrapped error from errors.As")
	}
}

func TestWithCodeLeavesNilAlone(t *testing.T) {
	t.Parallel()

	if err := errs.WithCode(errs.CodeUsage, nil); err != nil {
		t.Errorf("WithCode(nil) = %v, want nil", err)
	}
}

func TestCodedErrorReportsTheWrappedMessage(t *testing.T) {
	t.Parallel()

	err := errs.Usagef("--at is not supported over %s", "smtp")
	if got, want := err.Error(), "--at is not supported over smtp"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
