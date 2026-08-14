package clientfactory_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/clientfactory"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// Most invocations never make a request, so construction must not be what decides whether they
// run. "No API key" is a baffling answer to `mailkube --help`.
func TestNewNeedsNoCredential(t *testing.T) {
	t.Parallel()

	if f := clientfactory.New(clientfactory.Settings{}); f == nil {
		t.Fatal("New returned nothing")
	}
}

func TestClientReportsAMissingKeyAsAnAuthFailure(t *testing.T) {
	// Not parallel: the SDK falls back to the environment for a key, so this test has to be
	// sure there is not one.
	t.Setenv("MAILKUBE_API_KEY", "")

	_, err := clientfactory.New(clientfactory.Settings{}).Client()
	if err == nil {
		t.Fatal("a client was built with no credential")
	}
	if !errors.Is(err, mailkube.ErrNoAPIKey) {
		t.Errorf("error = %v, want the SDK's own ErrNoAPIKey", err)
	}
	if code := errs.CodeFor(err); code != errs.CodeAuth {
		t.Errorf("exit code = %d, want %d", code, errs.CodeAuth)
	}
}

func TestRequireAPIKeyChecksBeforeAnyWork(t *testing.T) {
	t.Parallel()

	if err := clientfactory.RequireAPIKey(clientfactory.Settings{APIKey: "mk_test"}); err != nil {
		t.Errorf("a configured key was rejected: %v", err)
	}

	for _, key := range []string{"", "   "} {
		err := clientfactory.RequireAPIKey(clientfactory.Settings{APIKey: key})
		if err == nil {
			t.Fatalf("RequireAPIKey(%q) accepted nothing", key)
		}
		if code := errs.CodeFor(err); code != errs.CodeAuth {
			t.Errorf("exit code = %d, want %d", code, errs.CodeAuth)
		}
	}
}

func TestClientIsBuiltOnce(t *testing.T) {
	t.Parallel()

	f := clientfactory.New(clientfactory.Settings{APIKey: "mk_test"})

	first, err := f.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	second, err := f.Client()
	if err != nil {
		t.Fatalf("second Client: %v", err)
	}
	if first != second {
		t.Error("a second client was built")
	}
}

// The SDK resolves paths by RFC 3986 reference resolution, where the base's last segment is
// replaced rather than appended to. Without the trailing slash a base of ".../mta/v1" plus a
// relative "emails" resolves to ".../mta/emails": the version segment vanishes and the user gets
// a 404 with nothing to explain it.
func TestNormalizeBaseURLKeepsThePathIntact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"missing slash", "https://api.example.com/mta/v1", "https://api.example.com/mta/v1/"},
		{"already correct", "https://api.example.com/mta/v1/", "https://api.example.com/mta/v1/"},
		{"bare host", "https://api.example.com", "https://api.example.com/"},
		{"surrounding space", "  https://api.example.com/v1  ", "https://api.example.com/v1/"},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := clientfactory.NormalizeBaseURL(tc.in); got != tc.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The normalisation is only worth anything if it reaches the wire, so this asserts the path the
// server actually receives rather than the string the helper returns.
func TestASlashlessBaseURLStillReachesTheVersionedPath(t *testing.T) {
	t.Parallel()

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"9f3b2c14","message_id":"<x@msg.example>"}`))
	}))
	defer server.Close()

	client, err := clientfactory.New(clientfactory.Settings{
		APIKey:  "mk_test",
		BaseURL: server.URL + "/mta/v1", // deliberately without the trailing slash
		Timeout: 5 * time.Second,
	}).Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}

	_, err = client.Emails.Send(context.Background(), mailkube.SendEmailParams{
		From: "hello@acme.com", To: []string{"alice@example.com"},
		Subject: "Hi", Text: "yo",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotPath != "/mta/v1/emails" {
		t.Errorf("the server was asked for %q, want /mta/v1/emails", gotPath)
	}
}

// Verbosity is two tiers, not three: -vv turns the SDK's own logging on, and the CLI adds no
// second logging stack that would have to redact credentials itself.
func TestSDKLoggingFollowsVerbosity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		verbosity int
		wantLog   bool
	}{
		{"quiet", 0, false},
		{"cli progress only", 1, false},
		{"sdk logging", clientfactory.VerboseSDK, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"9f3b2c14"}`))
			}))
			defer server.Close()

			var log strings.Builder
			client, err := clientfactory.New(clientfactory.Settings{
				APIKey:    "mk_test",
				BaseURL:   server.URL + "/",
				Verbosity: tc.verbosity,
				LogTo:     &log,
			}).Client()
			if err != nil {
				t.Fatalf("Client: %v", err)
			}

			_, err = client.Emails.Send(context.Background(), mailkube.SendEmailParams{
				From: "hello@acme.com", To: []string{"alice@example.com"},
				Subject: "Hi", Text: "yo",
			})
			if err != nil {
				t.Fatalf("Send: %v", err)
			}

			if logged := log.Len() > 0; logged != tc.wantLog {
				t.Errorf("logged = %v, want %v (%q)", logged, tc.wantLog, log.String())
			}
			// Whatever the tier, the credential never appears. The SDK redacts it at the
			// source, which is why the CLI has no redaction code of its own.
			if strings.Contains(log.String(), "mk_test") {
				t.Errorf("the API key was logged: %q", log.String())
			}
		})
	}
}

// The HTTP client is the only injection point, and it exists for tests. There is no flag that
// disables certificate verification, because a CLI that ships one grows a support answer telling
// people to use it.
func TestWithHTTPClientIsUsed(t *testing.T) {
	t.Parallel()

	var used bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"9f3b2c14"}`))
	}))
	defer server.Close()

	client, err := clientfactory.New(clientfactory.Settings{
		APIKey:  "mk_test",
		BaseURL: server.URL + "/",
	}).WithHTTPClient(&http.Client{Transport: recordingTransport{used: &used}}).Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}

	_, _ = client.Emails.Send(context.Background(), mailkube.SendEmailParams{
		From: "hello@acme.com", To: []string{"alice@example.com"},
		Subject: "Hi", Text: "yo",
	})

	if !used {
		t.Error("the supplied HTTP client was not used")
	}
}

type recordingTransport struct{ used *bool }

func (t recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	*t.used = true
	return http.DefaultTransport.RoundTrip(r)
}
