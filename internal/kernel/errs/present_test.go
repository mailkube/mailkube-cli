package errs_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

func TestDescribeCarriesTheServerFieldsThrough(t *testing.T) {
	t.Parallel()

	apiErr := &mailkube.APIError{
		ErrorName:  "quota_exceeded",
		Message:    "Monthly quota exhausted.",
		StatusCode: 422,
		RequestID:  "91bd07c2a4f84c7fa10d5e2b9c46f183",
		RetryAfter: 0,
	}

	d := errs.Describe(apiErr)

	if d.Name != "quota_exceeded" {
		t.Errorf("Name = %q", d.Name)
	}
	if d.Message != "Monthly quota exhausted." {
		t.Errorf("Message = %q, want the server's own words", d.Message)
	}
	if d.StatusCode != 422 {
		t.Errorf("StatusCode = %d", d.StatusCode)
	}
	if d.RequestID != "91bd07c2a4f84c7fa10d5e2b9c46f183" {
		t.Errorf("RequestID = %q", d.RequestID)
	}
	if d.Code != errs.CodeValidation {
		t.Errorf("Code = %d, want %d", d.Code, errs.CodeValidation)
	}
	if d.Retryable {
		t.Error("a quota failure is not retryable")
	}
}

func TestDescribeHandlesAnErrorThatNeverReachedTheServer(t *testing.T) {
	t.Parallel()

	d := errs.Describe(errs.Validationf("invalid tag name %q", "campaign name"))

	if d.Name != "" {
		t.Errorf("Name = %q, want empty for a local failure", d.Name)
	}
	if d.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", d.StatusCode)
	}
	if d.Message != `invalid tag name "campaign name"` {
		t.Errorf("Message = %q", d.Message)
	}
	if d.Code != errs.CodeValidation {
		t.Errorf("Code = %d", d.Code)
	}
}

func TestDescribeNilIsZero(t *testing.T) {
	t.Parallel()

	if got := errs.Describe(nil); !reflect.DeepEqual(got, errs.Detail{}) {
		t.Errorf("Describe(nil) = %+v, want the zero Detail", got)
	}
}

func TestRetryableCoversTheThreeTransientCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want bool
	}{
		{mailkube.ErrRateLimit, true},
		{mailkube.ErrServer, true},
		{mailkube.ErrConnection, true},
		{mailkube.ErrAuthentication, false},
		{mailkube.ErrNotFound, false},
		{errs.Usagef("bad flag"), false},
	}

	for _, tc := range tests {
		if got := errs.Describe(tc.err).Retryable; got != tc.want {
			t.Errorf("Describe(%v).Retryable = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestRenderBuildsTheFullReport(t *testing.T) {
	t.Parallel()

	d := errs.Describe(&mailkube.APIError{
		ErrorName:  "rate_limit_exceeded",
		Message:    "Too many requests. Please slow down.",
		StatusCode: 429,
		RequestID:  "8f2c1ad4e93b4c7fa10d5e2b9c46f183",
		RetryAfter: 3,
	})

	got := strings.Join(errs.Render(d, "✗"), "\n")
	want := strings.Join([]string{
		"✗ rate_limit_exceeded — Too many requests. Please slow down.",
		"  Retry after 3s.",
		"  request 8f2c1ad4e93b4c7fa10d5e2b9c46f183  ·  HTTP 429",
		"  Nothing is retried automatically. Re-run the command yourself to try again.",
	}, "\n")

	if got != want {
		t.Errorf("Render():\n%s\n\nwant:\n%s", got, want)
	}
}

// The request id is what a user quotes to support. An id shortened to fit a terminal is one that
// cannot be looked up, so the report prints all of it.
func TestRenderPrintsTheRequestIDInFull(t *testing.T) {
	t.Parallel()

	const id = "8f2c1ad4e93b4c7fa10d5e2b9c46f183"
	d := errs.Describe(&mailkube.APIError{ErrorName: "x", StatusCode: 500, RequestID: id})

	if !strings.Contains(strings.Join(errs.Render(d, "✗"), "\n"), id) {
		t.Error("the request id was abbreviated")
	}
}

func TestRenderStatesTheRetryPositionInBothDirections(t *testing.T) {
	t.Parallel()

	auth := errs.Describe(&mailkube.APIError{
		ErrorName: "invalid_api_key", Message: "The API key is not valid.", StatusCode: 403,
	})
	authReport := strings.Join(errs.Render(auth, "✗"), "\n")
	if !strings.Contains(authReport, "never retried automatically") {
		t.Errorf("an auth failure must say it is not retried:\n%s", authReport)
	}

	notFound := errs.Describe(mailkube.ErrNotFound)
	if note := strings.Join(errs.Render(notFound, "✗"), "\n"); strings.Contains(note, "retry") {
		t.Errorf("a not-found needs no retry advice:\n%s", note)
	}
}

// Whether re-running is safe depends on what the command does, so a command that knows replaces
// the generic sentence rather than the kernel guessing on its behalf.
func TestRenderLetsACommandReplaceTheRetryNote(t *testing.T) {
	t.Parallel()

	d := errs.Describe(mailkube.ErrRateLimit)
	d.RetryNote = "Re-run it, or use --idempotency-key to make a retry safe."

	got := strings.Join(errs.Render(d, "✗"), "\n")
	if !strings.Contains(got, "--idempotency-key") {
		t.Errorf("the command's own note was dropped:\n%s", got)
	}
	if strings.Contains(got, "Nothing is retried automatically") {
		t.Errorf("both notes were rendered:\n%s", got)
	}
}

func TestRenderUsesTheGlyphItIsGiven(t *testing.T) {
	t.Parallel()

	d := errs.Describe(errors.New("boom"))
	if got := errs.Render(d, "[x]")[0]; got != "[x] boom" {
		t.Errorf("headline = %q, want the ASCII glyph", got)
	}
}

func TestRenderIncludesCallerHints(t *testing.T) {
	t.Parallel()

	d := errs.Describe(errs.Validationf(`invalid tag name "campaign name"`))
	d.Hints = []string{
		"Tag names allow A-Z a-z 0-9 _ - only, max 16 characters.",
		"See: mailkube errors explain validation_error",
	}

	got := strings.Join(errs.Render(d, "✗"), "\n")
	want := strings.Join([]string{
		`✗ invalid tag name "campaign name"`,
		"  Tag names allow A-Z a-z 0-9 _ - only, max 16 characters.",
		"  See: mailkube errors explain validation_error",
	}, "\n")

	if got != want {
		t.Errorf("Render():\n%s\n\nwant:\n%s", got, want)
	}
}

func TestHeadlineDegradesWhenFieldsAreMissing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			"name and message",
			&mailkube.APIError{ErrorName: "not_acceptable", Message: "Nope.", StatusCode: 406},
			"✗ not_acceptable — Nope.",
		},
		{
			// APIError.Error() falls back to the name when there is no message, which would
			// otherwise render as "name — name".
			"name only",
			&mailkube.APIError{ErrorName: "not_acceptable", StatusCode: 406},
			"✗ not_acceptable",
		},
		{
			// And to "HTTP <status>" when there is neither.
			"neither",
			&mailkube.APIError{StatusCode: 406},
			"✗ HTTP 406",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errs.Render(errs.Describe(tc.err), "✗")[0]; got != tc.want {
				t.Errorf("headline = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatRetryAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		seconds int
		want    string
	}{
		{-1, "0s"},
		{0, "0s"},
		{1, "1s"},
		{59, "59s"},
		{60, "1m"},
		{90, "1m30s"},
		{3600, "60m"},
	}

	for _, tc := range tests {
		if got := errs.FormatRetryAfter(tc.seconds); got != tc.want {
			t.Errorf("FormatRetryAfter(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

// The JSON error shape is a semver-stable contract, so assert the bytes rather than the struct.
func TestEnvelopeMarshalsToTheDocumentedShape(t *testing.T) {
	t.Parallel()

	d := errs.Describe(&mailkube.APIError{
		ErrorName:  "quota_exceeded",
		Message:    "Monthly quota exhausted.",
		StatusCode: 422,
		RequestID:  "91bd07c2a4f84c7fa10d5e2b9c46f183",
		RetryAfter: 30,
	})

	got, err := json.Marshal(errs.Envelope(d))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := `{"error":{"name":"quota_exceeded","message":"Monthly quota exhausted.",` +
		`"statusCode":422,"requestId":"91bd07c2a4f84c7fa10d5e2b9c46f183"}}`
	if string(got) != want {
		t.Errorf("json = %s\nwant  %s", got, want)
	}
}

// A local failure has no name, status or request id, and the envelope omits them rather than
// emitting empty strings a consumer would have to distinguish from real values.
func TestEnvelopeOmitsFieldsAnOfflineFailureDoesNotHave(t *testing.T) {
	t.Parallel()

	got, err := json.Marshal(errs.Envelope(errs.Describe(errs.Usagef("--all conflicts with --page"))))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := `{"error":{"message":"--all conflicts with --page"}}`
	if string(got) != want {
		t.Errorf("json = %s\nwant  %s", got, want)
	}
}
