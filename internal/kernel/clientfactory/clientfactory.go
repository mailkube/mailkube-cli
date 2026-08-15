// Package clientfactory builds the SDK client, and is the only place that does.
//
// Everything the CLI knows how to do over the network, it does through mailkube-go. Concentrating
// construction here is what makes that true in practice rather than by convention: a command
// receives a client it did not build, so there is nowhere for a hand-rolled request to appear, and
// there is exactly one place that decides what the client is configured with.
package clientfactory

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	mailkube "github.com/mailkube/mailkube-go"
)

// Settings is what the CLI resolved before any client was needed.
//
// The values arrive already resolved, because precedence — flag, then environment, then config
// file, then default — is one rule that applies to every setting, and splitting it between the
// composition root and this package would give two answers to "why is it using that base URL".
type Settings struct {
	// APIKey authenticates the REST transport. Empty means none was configured.
	APIKey string
	// BaseURL overrides the SDK's default endpoint. Empty means use the SDK's.
	BaseURL string
	// Timeout bounds a single request.
	Timeout time.Duration
	// UserAgentSuffix identifies this tool after the SDK's own token.
	UserAgentSuffix string
	// Verbosity selects what is logged; see VerboseSDK.
	Verbosity int
	// LogTo is where SDK logging goes when it is switched on. It is the error stream: the
	// success stream carries the payload and nothing else, and a request log interleaved into
	// a JSON document would break every caller that parses it.
	LogTo io.Writer
}

// VerboseSDK is the verbosity at which the SDK's request and response logging is switched on.
//
// There are two tiers, not three. The SDK logs method, URL, status and duration with its own
// redaction already applied, and it logs no bodies at all — so a third tier could only be built by
// adding a second logging stack in the CLI, which would then have to redact Authorization itself.
// One redaction implementation, at the source, is the point.
const VerboseSDK = 2

// Factory builds the SDK client on first use.
//
// Construction is deferred because most invocations never make a request. `--help`, `version`,
// `completion`, `webhooks verify` and every usage error would otherwise fail on a missing API key
// before doing the thing they were asked to do, and "no API key" is a baffling answer to
// `mailkube --help`.
type Factory struct {
	settings Settings
	http     *http.Client

	once   sync.Once
	client *mailkube.Client
	err    error
}

// New returns a Factory that will build a client from these settings.
func New(settings Settings) *Factory {
	return &Factory{settings: settings}
}

// WithHTTPClient returns a Factory using a supplied HTTP client.
//
// This is the seam the tests use, and the only one: there is no flag that disables certificate
// verification. A CLI that ships one grows a support answer telling people to use it, and that
// answer outlives whatever situation prompted it.
func (f *Factory) WithHTTPClient(c *http.Client) *Factory {
	f.http = c
	return f
}

// Client returns the SDK client, building it once.
//
// A missing API key surfaces as the SDK's own ErrNoAPIKey, which the exit-code chain already maps
// to the authentication code. Inventing a second missing-credential error here would mean two
// spellings of one condition, and only one of them tested.
func (f *Factory) Client() (*mailkube.Client, error) {
	f.once.Do(func() {
		f.client, f.err = mailkube.New(f.options()...)
	})
	return f.client, f.err
}

// options maps resolved settings onto the SDK's published options.
func (f *Factory) options() []mailkube.Option {
	opts := []mailkube.Option{
		mailkube.WithAPIKey(f.settings.APIKey),
		mailkube.WithUserAgentSuffix(f.settings.UserAgentSuffix),
	}

	if f.settings.BaseURL != "" {
		opts = append(opts, mailkube.WithBaseURL(NormalizeBaseURL(f.settings.BaseURL)))
	}
	if f.settings.Timeout > 0 {
		opts = append(opts, mailkube.WithTimeout(f.settings.Timeout))
	}
	if f.http != nil {
		opts = append(opts, mailkube.WithHTTPClient(f.http))
	}
	if logger := f.logger(); logger != nil {
		opts = append(opts, mailkube.WithLogger(logger))
	}
	return opts
}

// logger returns the SDK's logger for this verbosity, or nil to leave the SDK silent.
//
// Nil rather than a discarding logger, because the SDK already resolves an absent logger to one
// that drops everything and skips building the record at all. Handing it a second one would add a
// layer that exists only to be inert.
func (f *Factory) logger() *slog.Logger {
	if f.settings.Verbosity < VerboseSDK || f.settings.LogTo == nil {
		return nil
	}
	// The SDK emits its request and response records at debug level, so anything stricter
	// switches on a logger that never logs.
	return slog.New(slog.NewTextHandler(f.settings.LogTo, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// NormalizeBaseURL makes a user-supplied base URL safe to resolve relative paths against.
//
// The SDK joins paths by RFC 3986 reference resolution, where the last segment of the base is
// replaced rather than appended to. So a base of ".../mta/v1" with no trailing slash plus a
// relative "emails" resolves to ".../mta/emails": the version segment silently disappears and the
// user gets a 404 with nothing to explain it. The SDK's own default carries the slash for this
// exact reason, and its origin check compares scheme and host only, so it cannot catch a mangled
// path.
//
// One character, one place. A user who copies a base URL out of a browser address bar without the
// trailing slash is doing an ordinary thing, and it should not cost them an afternoon.
func NormalizeBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasSuffix(trimmed, "/") {
		return trimmed
	}
	return trimmed + "/"
}

// RequireAPIKey reports a missing API key before a command does any work.
//
// It exists so a command that is going to need a credential can say so up front, naming the ways
// to supply one, rather than letting the SDK's error surface after arguments have been read and
// files opened.
func RequireAPIKey(settings Settings) error {
	if strings.TrimSpace(settings.APIKey) != "" {
		return nil
	}
	return missingKey{}
}

// missingKey is the SDK's missing-credential error, told in the CLI's terms.
//
// It wraps rather than replaces, so errors.Is still finds ErrNoAPIKey and the exit chain still
// maps it to the authentication code: this is one condition with one identity, not a second
// spelling of it. Only the sentence changes, and it has to — the SDK's own message names a Go
// constructor option, which is sound advice for a program embedding the library and no advice at
// all for someone who typed a command.
type missingKey struct{}

// Error implements error.
func (missingKey) Error() string {
	return "no API key configured\n" +
		"Run `mailkube auth login`, or pass --api-key, or set MAILKUBE_API_KEY."
}

// Unwrap keeps the SDK's sentinel reachable through errors.Is.
func (missingKey) Unwrap() error { return mailkube.ErrNoAPIKey }
