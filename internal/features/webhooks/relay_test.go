package webhooks_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/webhooks"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// upstream is a local application standing in for the one being developed.
type upstream struct {
	*httptest.Server

	mu       sync.Mutex
	requests []received
}

// received is one forwarded delivery, as the application saw it.
type received struct {
	body    string
	headers http.Header
}

// newUpstream starts an application that answers with a fixed status.
func newUpstream(t *testing.T, status int) *upstream {
	t.Helper()

	app := &upstream{}
	app.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)

		app.mu.Lock()
		app.requests = append(app.requests, received{body: string(body), headers: r.Header.Clone()})
		app.mu.Unlock()

		w.WriteHeader(status)
	}))
	t.Cleanup(app.Close)
	return app
}

// wait blocks until the application has seen n requests, or gives up.
//
// Forwarding is asynchronous by default, so the delivery is acknowledged before the local
// application has necessarily been reached. A test that read the requests straight after the
// delivery would be testing the scheduler.
func (u *upstream) wait(t *testing.T, n int) []received {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		u.mu.Lock()
		got := len(u.requests)
		u.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]received(nil), u.requests...)
}

func TestAForwardArrivesWithTheSignatureThatCameWithIt(t *testing.T) {
	t.Parallel()

	// The whole point of forwarding rather than re-emitting: the local application runs exactly
	// the verification it will run in production, against exactly the bytes the platform sent.
	// Re-encoding the body or dropping a header would make that verification fail, and the
	// developer would be debugging our relay instead of their handler.
	app := newUpstream(t, http.StatusNoContent)
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")

	run := start(t, running(), "--local", "--forward", app.URL, "--exit-after", "1")
	run.deliver(t, "d1", body)
	run.wait(t)

	forwarded := app.wait(t, 1)
	if len(forwarded) != 1 {
		t.Fatalf("the application saw %d requests, want 1", len(forwarded))
	}
	if forwarded[0].body != body {
		t.Errorf("the forwarded body is not what arrived:\n%s", forwarded[0].body)
	}

	// Verifying it here rather than comparing header strings: the assertion that matters is
	// that a receiver can still check the signature, not that three headers were copied.
	if _, err := mailkube.VerifySignature([]byte(forwarded[0].body),
		forwarded[0].headers, secret, time.Minute); err != nil {
		t.Errorf("the forwarded delivery no longer verifies: %v", err)
	}
	if !strings.Contains(run.errOut.String(), "1 forwarded") {
		t.Errorf("the summary does not count the forward:\n%s", run.errOut)
	}
}

func TestAForwardTargetThatRefusesIsReportedAndDoesNotStopTheRun(t *testing.T) {
	t.Parallel()

	// The delivery was still received and is still the platform's business; what failed is the
	// developer's own application, and saying so is more useful than failing the listener.
	app := newUpstream(t, http.StatusInternalServerError)

	run := start(t, running(), "--local", "--forward", app.URL, "--exit-after", "1")
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.wait(t)
	app.wait(t, 1)
	run.stop(t)

	if run.code != errs.CodeOK {
		t.Fatalf("a failing forward failed the run: %d %s", run.code, run.errOut)
	}
	if !strings.Contains(run.errOut.String(), "500") {
		t.Errorf("the failure does not name the status:\n%s", run.errOut)
	}
}

func TestARedeliveryIsNotForwardedTwiceUnlessAskedFor(t *testing.T) {
	t.Parallel()

	// The local application already has this one. Sending it again is exactly the duplicate
	// that deduplicating exists to absorb, and an application that is not idempotent would act
	// on it twice.
	app := newUpstream(t, http.StatusNoContent)
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")

	run := start(t, running(), "--local", "--forward", app.URL)
	run.deliver(t, "d1", body)
	run.deliver(t, "d1", body)
	app.wait(t, 1)
	run.stop(t)

	if got := len(app.wait(t, 1)); got != 1 {
		t.Errorf("the application saw %d requests, want 1", got)
	}
}

func TestForwardDuplicatesSendsTheRedeliveryOnDeliberately(t *testing.T) {
	t.Parallel()

	app := newUpstream(t, http.StatusNoContent)
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")

	run := start(t, running(), "--local", "--forward", app.URL, "--forward-duplicates")
	run.deliver(t, "d1", body)
	run.deliver(t, "d1", body)
	app.wait(t, 2)
	run.stop(t)

	if got := len(app.wait(t, 2)); got != 2 {
		t.Errorf("the application saw %d requests, want 2", got)
	}
	// The duplicate is still shown as a duplicate: forwarding it does not make it new.
	if !strings.Contains(run.errOut.String(), "1 duplicate") {
		t.Errorf("the redelivery stopped being reported as one:\n%s", run.errOut)
	}
}

func TestAForwardIsNotWaitedOnUnlessAskedFor(t *testing.T) {
	t.Parallel()

	// The platform allows ten seconds and disables an endpoint whose first-attempt acceptance
	// falls below sixty per cent over a day. Blocking the acknowledgement on a slow local
	// application would eventually switch off a real endpoint, so the default cannot block.
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer slow.Close()

	run := start(t, running(), "--local", "--forward", slow.URL, "--exit-after", "1")
	// This returns only when the listener has acknowledged, which must not be waiting on the
	// application above.
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	close(release)
	run.wait(t)

	if run.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", run.code, run.errOut)
	}
}

func TestACaptureCanStillBeVerifiedAfterwards(t *testing.T) {
	t.Parallel()

	// The property that makes a capture a capture rather than a log. A signature is computed
	// over exact bytes, so anything that re-encodes the payload on the way to the file, or on
	// the way back out of it, makes a genuine delivery look forged.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	body := `{"type":"email.sent",` + "\n" + `  "created_at":"2026-08-15T14:02:40Z","data":{"email_id":"9f3b2c14"}}`

	run := start(t, running(), "--local", "--record", path, "--exit-after", "1")
	run.deliver(t, "d1", body)
	run.wait(t)

	lines := strings.Split(strings.TrimSpace(readFile(t, path)), "\n")
	if len(lines) != 1 {
		t.Fatalf("the capture holds %d lines, want 1", len(lines))
	}

	var got webhooks.Capture
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("the capture is not one JSON document per line: %v", err)
	}
	if got.Body != body {
		t.Fatalf("the captured body is not byte-for-byte what arrived:\n%q\n%q", got.Body, body)
	}

	headers := mailkube.HeaderFunc(func(name string) string {
		switch name {
		case "X-Webhook-Id":
			return got.ID
		case "X-Webhook-Ts":
			return got.Timestamp
		case "X-Webhook-Sig":
			return got.Signature
		}
		return ""
	})
	if _, err := mailkube.VerifySignature([]byte(got.Body), headers, secret, time.Hour); err != nil {
		t.Errorf("the captured delivery no longer verifies: %v", err)
	}
}

func TestACaptureIsAppendedAndNotTruncated(t *testing.T) {
	t.Parallel()

	// Restarting a listener is what people do while chasing a problem. A capture destroyed by
	// the restart is the one they were about to read.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")

	for _, id := range []string{"d1", "d2"} {
		run := start(t, running(), "--local", "--record", path, "--exit-after", "1")
		run.deliver(t, id, body)
		run.wait(t)
	}

	if got := strings.Count(strings.TrimSpace(readFile(t, path)), "\n") + 1; got != 2 {
		t.Errorf("the capture holds %d lines after two runs, want 2", got)
	}
}

func TestACaptureIsNotReadableByAnyoneElse(t *testing.T) {
	t.Parallel()

	if isWindows() {
		t.Skip("permissions are an ACL on Windows, checked by doctor rather than by mode")
	}

	// A capture holds recipient addresses and subjects, which belong to whoever was sent the
	// mail rather than to the developer debugging it.
	path := filepath.Join(t.TempDir(), "events.jsonl")
	run := start(t, running(), "--local", "--record", path, "--exit-after", "1")
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.wait(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("capture mode = %04o, want 0600", mode)
	}
}

func TestForwardRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		says string
	}{
		{
			// Forwarding re-sends someone else's signed payload. Pointing it off this
			// machine turns a development tool into a relay, and the mistake that does it
			// is a typo in a hostname rather than a decision anyone made.
			name: "a target that is not on this machine",
			args: []string{"--local", "--forward", "https://example.com/hooks"},
			says: "not on this machine",
		},
		{
			name: "a target that is not a URL",
			args: []string{"--local", "--forward", "localhost:3000"},
			says: "not an http or https URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, reported := refuse(t, running(), tt.args...)
			if code != errs.CodeUsage {
				t.Errorf("exit code = %d, want %d: %s", code, errs.CodeUsage, reported)
			}
			if !strings.Contains(reported, tt.says) {
				t.Errorf("the refusal does not say %q:\n%s", tt.says, reported)
			}
		})
	}
}

func TestAForwardOffThisMachineIsAllowedWhenForced(t *testing.T) {
	t.Parallel()

	// The guard is against the typo, not against the intent. Someone who means it says so.
	app := newUpstream(t, http.StatusNoContent)

	run := start(t, running(), "--local", "--force",
		"--forward", app.URL, "--exit-after", "1")
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.wait(t)

	if got := len(app.wait(t, 1)); got != 1 {
		t.Errorf("the forced forward did not arrive: %d requests", got)
	}
}

func TestLocalAndPublicURLCannotBothBeGiven(t *testing.T) {
	t.Parallel()

	code, reported := refuse(t, running(), "--local", "--public-url", "https://a1b2.example.com")
	if code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d: %s", code, errs.CodeUsage, reported)
	}
	if !strings.Contains(reported, "opposite things") {
		t.Errorf("the refusal does not explain the contradiction:\n%s", reported)
	}
}

func TestLocalSaysNothingRealCanArrive(t *testing.T) {
	t.Parallel()

	// Someone reaching for --local because the tunnel was inconvenient has not worked around
	// the tunnel; they have opted out of receiving anything real, and the banner has to say so
	// or they will sit waiting for an event that cannot come.
	run := start(t, running(), "--local")
	run.stop(t)

	banner := run.errOut.String()
	if !strings.Contains(banner, "nothing from Mailkube can reach this") {
		t.Errorf("the banner does not say what --local costs:\n%s", banner)
	}
	if !strings.Contains(banner, "webhooks simulate") {
		t.Errorf("the banner does not name the way to send something:\n%s", banner)
	}
}

func TestTheSuggestedSimulateCommandIsOneThatRuns(t *testing.T) {
	t.Parallel()

	// A container binds a wildcard, because a published port cannot reach loopback there. But a
	// wildcard answers "accept on which interfaces" and names no destination: simulate's own
	// guard refuses one, and Windows will not connect to it at all. So the suggestion is taken
	// out of the banner and run, rather than compared against an expected string. What is being
	// asserted is not the wording but that the command works, which is the only thing a
	// copy-pasteable line has to do.
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()

	_, port, err := net.SplitHostPort(endpoint.Listener.Addr().String())
	if err != nil {
		t.Fatalf("the standing-in endpoint has no port: %v", err)
	}

	run := start(t, running(), "--local", "--host", "0.0.0.0", "--port", port)
	run.stop(t)

	suggested := suggestedTarget(t, run.errOut.String())
	if code, _, errOut := invoke(t, running(), "simulate",
		"--url", suggested, "--event", "email.sent"); code != errs.CodeOK {
		t.Errorf("the banner suggests %s, which exits %d: %s", suggested, code, errOut)
	}
}

// suggestedTarget reads the --url out of the command a banner tells the user to run.
func suggestedTarget(t *testing.T, banner string) string {
	t.Helper()

	for _, line := range strings.Split(banner, "\n") {
		if !strings.Contains(line, "webhooks simulate") {
			continue
		}
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "--url" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	t.Fatalf("the banner suggests no simulate command:\n%s", banner)
	return ""
}

// isWindows reports whether the permission model is the ACL one.
func isWindows() bool { return os.PathSeparator == '\\' }

// readFile returns a file's contents, failing the test if it cannot.
func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}
