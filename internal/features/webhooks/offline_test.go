package webhooks_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/webhooks"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// invoke runs one webhooks subcommand to completion and captures both streams.
//
// Unlike the listener helper this expects the command to return, so it is the shape for `verify`
// and `simulate`, neither of which waits for anything.
func invoke(t *testing.T, opts testsupport.TestOptions, args ...string) (errs.Code, string, string) {
	t.Helper()

	if opts.Globals == nil {
		opts.Globals = &settings.Globals{Output: "text"}
	}
	deps, out, errOut := testsupport.TestDeps(t, opts)

	f := webhooks.New()
	f.Bind = func(address string) (net.Listener, error) {
		t.Errorf("an offline command opened a socket on %s", address)
		return nil, nil
	}

	cmd := f.Command(deps)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	code := errs.CodeOK
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		detail := errs.Describe(err)
		code = detail.Code
		errOut.WriteString(strings.Join(errs.Render(detail, deps.Caps.Glyphs.Cross), "\n"))
	}
	return code, out.String(), errOut.String()
}

// signed writes a payload to a file and returns it with the headers that authenticate it.
//
// The age is measured against the real clock rather than the injected one, because freshness is
// the one thing here the SDK checks against the wall clock. A fixture stamped with the fixed test
// instant would be a day stale by the time the verifier looked at it, and every test would be
// asserting the clock rather than the signature.
func signed(t *testing.T, body string, age time.Duration) (path, id, timestamp, signature string) {
	t.Helper()

	path = filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the payload: %v", err)
	}

	id = "0f7a1c33-1111-2222-3333-444455556666"
	timestamp = time.Now().Add(-age).UTC().Format(time.RFC3339)
	return path, id, timestamp, mailkube.Sign(id, timestamp, []byte(body), secret)
}

func TestVerifyAcceptsADeliveryThatCheckedOut(t *testing.T) {
	t.Parallel()

	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	path, id, timestamp, signature := signed(t, body, 0)

	code, out, errOut := invoke(t, running(), "verify",
		"--body", "@"+path, "--id", id, "--ts", timestamp, "--sig", signature)

	if code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", code, errOut)
	}
	for _, want := range []string{"Signature valid", "email.sent", id} {
		if !strings.Contains(out, want) {
			t.Errorf("the verdict does not carry %q:\n%s", want, out)
		}
	}
}

func TestVerifyRefusesAPayloadThatWasChanged(t *testing.T) {
	t.Parallel()

	// The whole purpose. A signature covers exact bytes, so one altered character has to be
	// the difference between accepted and refused.
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	_, id, timestamp, signature := signed(t, body, 0)

	tampered := filepath.Join(t.TempDir(), "tampered.json")
	if err := os.WriteFile(tampered, []byte(strings.Replace(body, "alice", "mallory", 1)), 0o600); err != nil {
		t.Fatalf("writing the payload: %v", err)
	}

	code, out, _ := invoke(t, running(), "verify",
		"--body", "@"+tampered, "--id", id, "--ts", timestamp, "--sig", signature)

	if code != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", code, errs.CodeValidation)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("a refused payload produced a payload of its own:\n%s", out)
	}
}

func TestVerifyWithNoToleranceIsWhatMakesACaptureReplayable(t *testing.T) {
	t.Parallel()

	// The common use is a capture from a previous run, where the timestamp is legitimately old.
	// A verifier that failed on everything older than five minutes could not do the one thing
	// an offline verifier is for.
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	path, id, timestamp, signature := signed(t, body, 24*time.Hour)

	args := []string{"verify", "--body", "@" + path, "--id", id, "--ts", timestamp, "--sig", signature}

	if code, _, _ := invoke(t, running(), args...); code != errs.CodeValidation {
		t.Errorf("a day-old capture was accepted at the default tolerance: exit %d", code)
	}
	if code, _, errOut := invoke(t, running(), append(args, "--tolerance", "0")...); code != errs.CodeOK {
		t.Errorf("--tolerance 0 did not accept the capture: exit %d %s", code, errOut)
	}
}

func TestVerifyRefusals(t *testing.T) {
	t.Parallel()

	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	path, id, timestamp, signature := signed(t, body, 0)

	tests := []struct {
		name string
		args []string
		env  bool
		code errs.Code
		says string
	}{
		{
			name: "no payload",
			args: []string{"verify", "--id", id, "--ts", timestamp, "--sig", signature},
			env:  true,
			code: errs.CodeUsage,
			says: "--body is required",
		},
		{
			name: "no id",
			args: []string{"verify", "--body", "@" + path, "--ts", timestamp, "--sig", signature},
			env:  true,
			code: errs.CodeUsage,
			says: "--id is required",
		},
		{
			name: "no secret anywhere",
			args: []string{"verify", "--body", "@" + path, "--id", id, "--ts", timestamp, "--sig", signature},
			code: errs.CodeConfig,
			says: "no signing secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := testsupport.TestOptions{Globals: &settings.Globals{Output: "text"}}
			if tt.env {
				opts.Env = map[string]string{settings.EnvWebhookSecret: secret}
			}

			code, _, errOut := invoke(t, opts, tt.args...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d: %s", code, tt.code, errOut)
			}
			if !strings.Contains(errOut, tt.says) {
				t.Errorf("the refusal does not say %q:\n%s", tt.says, errOut)
			}
		})
	}
}

func TestSimulateSendsSomethingTheReceiverWillAccept(t *testing.T) {
	t.Parallel()

	// The point of simulating rather than curling: the delivery is signed the way the platform
	// signs, so the handler runs with verification on, which is the code that will run in
	// production.
	var got struct {
		body    string
		headers http.Header
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.body, got.headers = string(raw), r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()

	code, out, errOut := invoke(t, running(), "simulate", "--url", endpoint.URL, "--event", "email.bounced")
	if code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", code, errOut)
	}

	// A window wide enough to ignore freshness. simulate stamps the delivery from the injected
	// clock so its own output is reproducible, and the test clock is fixed, so what is being
	// asserted here is the signature construction rather than the calendar.
	event, err := mailkube.Verify([]byte(got.body), got.headers, secret, 100*365*24*time.Hour)
	if err != nil {
		t.Fatalf("the simulated delivery does not verify: %v", err)
	}
	if event.Type != "email.bounced" {
		t.Errorf("event type = %q", event.Type)
	}
	if !strings.Contains(out, "email.bounced") {
		t.Errorf("the report does not name what was sent:\n%s", out)
	}
}

func TestSimulateSpellsTheTimestampTheWayThePlatformDoes(t *testing.T) {
	t.Parallel()

	// Microseconds and a numeric offset rather than "Z". The timestamp is part of the signed
	// input, so a receiver with a strict Z-only parser fails on the real thing; a simulation
	// that sent the easier spelling would hide that until production.
	var sentTimestamp string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentTimestamp = r.Header.Get("X-Webhook-Ts")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()

	if code, _, errOut := invoke(t, running(), "simulate",
		"--url", endpoint.URL, "--event", "email.sent"); code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", code, errOut)
	}

	if !strings.HasSuffix(sentTimestamp, "+00:00") {
		t.Errorf("timestamp %q does not carry the numeric offset the platform sends", sentTimestamp)
	}
	if !strings.Contains(sentTimestamp, ".") {
		t.Errorf("timestamp %q carries no sub-second part", sentTimestamp)
	}
	if _, err := time.Parse(time.RFC3339, sentTimestamp); err != nil {
		t.Errorf("timestamp %q is not RFC 3339: %v", sentTimestamp, err)
	}
}

func TestSimulateWithoutASecretSendsSomethingThatShouldBeRefused(t *testing.T) {
	t.Parallel()

	// Testing the rejection path is a real thing to want: a handler that accepts an unsigned
	// delivery is the defect this lets you find.
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Webhook-Sig") != "" {
			t.Error("an unsigned simulation carried a signature")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer endpoint.Close()

	opts := testsupport.TestOptions{Globals: &settings.Globals{Output: "text"}}
	code, out, _ := invoke(t, opts, "simulate", "--url", endpoint.URL, "--event", "email.sent")

	// A refusal from the endpoint is the endpoint's answer, reported as such: exit 8 says the
	// far side said no, and the report still describes what was sent.
	if code != errs.CodeServer {
		t.Errorf("exit code = %d, want %d", code, errs.CodeServer)
	}
	if !strings.Contains(out, "should refuse it") {
		t.Errorf("the report does not say the delivery was unsigned:\n%s", out)
	}
}

func TestSimulateCarriesASuppliedPayloadUnaltered(t *testing.T) {
	t.Parallel()

	var delivered string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		delivered = string(raw)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()

	body := `{"type":"email.teleported","created_at":"2026-08-15T14:00:00Z","data":{"destination":"mars"}}`
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the payload: %v", err)
	}

	code, out, errOut := invoke(t, running(), "simulate", "--url", endpoint.URL, "--file", "@"+path)
	if code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", code, errOut)
	}
	if delivered != body {
		t.Errorf("the payload was altered on the way out:\n%s", delivered)
	}
	// The file's own type is reported, not a flag's: what the endpoint received is the file.
	if !strings.Contains(out, "email.teleported") {
		t.Errorf("the report does not describe the delivery that happened:\n%s", out)
	}
}

func TestSimulateRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		code errs.Code
		says string
	}{
		{
			// A list beats a guess: the types are known, and someone who mistyped one
			// wants to see the set rather than the spelling rules.
			name: "no event type",
			args: []string{"simulate", "--url", "http://127.0.0.1:4318"},
			code: errs.CodeUsage,
			says: "email.bounced",
		},
		{
			name: "an event type with no sample",
			args: []string{"simulate", "--url", "http://127.0.0.1:4318", "--event", "email.teleported"},
			code: errs.CodeUsage,
			says: "no sample payload",
		},
		{
			name: "no target",
			args: []string{"simulate", "--event", "email.sent"},
			code: errs.CodeUsage,
			says: "--url is required",
		},
		{
			// The same guard as --forward, for the same reason: this posts a signed
			// payload, and pointing it at a stranger makes it something else.
			name: "a target that is not on this machine",
			args: []string{"simulate", "--url", "https://example.com/hooks", "--event", "email.sent"},
			code: errs.CodeUsage,
			says: "not on this machine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, _, errOut := invoke(t, running(), tt.args...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d: %s", code, tt.code, errOut)
			}
			if !strings.Contains(errOut, tt.says) {
				t.Errorf("the refusal does not say %q:\n%s", tt.says, errOut)
			}
		})
	}
}

func TestEveryModelledEventTypeHasASamplePayload(t *testing.T) {
	t.Parallel()

	// The anchor between the fixtures and the SDK. An event type the platform gained and this
	// binary models is one someone will reach for, and finding no sample would send them to
	// hand-writing a payload the CLI could have handed them.
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	// Cleanup rather than defer: the subtests below are parallel, so they run after this
	// function returns, and a deferred Close would shut the endpoint before any of them used it.
	t.Cleanup(endpoint.Close)

	for _, eventType := range mailkube.EventTypes() {
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()

			code, _, errOut := invoke(t, running(), "simulate", "--url", endpoint.URL, "--event", eventType)
			if code != errs.CodeOK {
				t.Errorf("no usable sample for %s: exit %d %s", eventType, code, errOut)
			}
		})
	}
}
