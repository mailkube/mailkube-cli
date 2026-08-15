package webhooks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/webhooks"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// secret is the signing secret every test in this file uses.
const secret = "s3cr3t"

// listener is one run of `webhooks listen`, with the address it actually bound.
type listener struct {
	// address is where the test posts, which is not the address the flags asked for: the
	// operating system chose a free port so that this suite can run in parallel.
	address string

	out, errOut *bytes.Buffer
	code        errs.Code
	done        chan struct{}
	cancel      context.CancelFunc

	// client is this listener's own, and so is its transport. Both halves matter: a Client with
	// no Transport uses the shared default one, and httptest.Server.Close calls
	// CloseIdleConnections on that, so a parallel test tearing down a forward target breaks a
	// request this one has in flight. Giving each listener a Client but leaving the Transport
	// shared looks like isolation and provides none.
	client *http.Client
}

// start runs the listener in the background and waits until it is accepting connections.
//
// The command is driven through its own cobra tree rather than by calling the runner, because the
// flag defaults are part of what is being tested: a default that stopped being applied would
// otherwise show up as a behaviour change nothing failed on.
func start(t *testing.T, opts testsupport.TestOptions, args ...string) *listener {
	t.Helper()

	if opts.Globals == nil {
		opts.Globals = &settings.Globals{Output: "text"}
	}
	deps, out, errOut := testsupport.TestDeps(t, opts)

	bound := make(chan string, 1)
	f := webhooks.New()
	f.Bind = func(ctx context.Context, _ string) (net.Listener, error) {
		var lc net.ListenConfig
		socket, err := lc.Listen(ctx, "tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		bound <- socket.Addr().String()
		return socket, nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	run := &listener{
		out: out, errOut: errOut, done: make(chan struct{}), cancel: cancel,
		client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{}},
	}

	cmd := f.Command(deps)
	cmd.SetArgs(append([]string{"listen"}, args...))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	go func() {
		defer close(run.done)
		if err := cmd.ExecuteContext(ctx); err != nil {
			run.code = errs.CodeFor(err)
		}
	}()

	select {
	case run.address = <-bound:
	case <-time.After(5 * time.Second):
		t.Fatal("the listener never bound")
	}
	t.Cleanup(func() { cancel(); <-run.done })
	return run
}

// wait blocks until the run has ended and its streams are safe to read.
func (l *listener) wait(t *testing.T) {
	t.Helper()
	select {
	case <-l.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the listener never stopped")
	}
}

// stop cancels the run the way a signal would, then waits for it.
func (l *listener) stop(t *testing.T) {
	t.Helper()
	l.cancel()
	l.wait(t)
}

// deliver posts one signed delivery, exactly as the platform would.
//
// Signing goes through the SDK's own Sign rather than a second HMAC written here, so a test
// cannot pass against a construction the verifier does not use.
func (l *listener) deliver(t *testing.T, id, body string) int {
	t.Helper()
	timestamp := time.Now().UTC().Format(time.RFC3339)
	return l.post(t, id, timestamp, mailkube.Sign(id, timestamp, []byte(body), secret), body, "/")
}

// post sends a delivery with whatever headers the test wants, including wrong ones.
func (l *listener) post(t *testing.T, id, timestamp, signature, body, path string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://"+l.address+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the delivery: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if id != "" {
		req.Header.Set("X-Webhook-Id", id)
	}
	if timestamp != "" {
		req.Header.Set("X-Webhook-Ts", timestamp)
	}
	if signature != "" {
		req.Header.Set("X-Webhook-Sig", signature)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatalf("posting the delivery: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// probe performs the registration handshake and returns the status and what was echoed.
func (l *listener) probe(t *testing.T, query string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://"+l.address+"/?"+query, nil)
	if err != nil {
		t.Fatalf("building the probe: %v", err)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatalf("probing: %v", err)
	}
	defer resp.Body.Close()

	echoed, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(echoed)
}

// running is the environment of someone with a secret in their environment.
func running() testsupport.TestOptions {
	return testsupport.TestOptions{
		Env:     map[string]string{settings.EnvWebhookSecret: secret},
		Globals: &settings.Globals{Output: "text"},
	}
}

// sent is a delivered email.sent payload.
func sent(emailID, recipient string) string {
	return `{"type":"email.sent","created_at":"2026-08-15T14:02:40Z","data":{` +
		`"email_id":"` + emailID + `","to":["` + recipient + `"],"subject":"Welcome",` +
		`"sent":{"recipient":"` + recipient + `","timestamp":"2026-08-15T14:02:40Z"}}}`
}

// bounced is a delivered email.bounced payload, whose reason is remote text.
func bounced(reason string) string {
	return `{"type":"email.bounced","created_at":"2026-08-15T14:04:02Z","data":{` +
		`"email_id":"7c1a9e33-1111-2222-3333-444455556666","to":["bob@example.com"],` +
		`"bounce":{"recipient":"bob@example.com","timestamp":"2026-08-15T14:04:02Z",` +
		`"code":550,"reason":` + quote(reason) + `}}}`
}

// quote renders a string as a JSON string literal.
func quote(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestARegistrationProbeIsAnsweredWithItsOwnChallenge(t *testing.T) {
	t.Parallel()

	// The handshake is the reason this command has to be running before an endpoint can be
	// created: the platform will not save a URL that does not echo its challenge.
	run := start(t, running(), "--public-url", "https://a1b2.example.com")
	status, echoed := run.probe(t, "hub.mode=subscribe&hub.challenge=a3f91c07d2b4e6f8")
	run.stop(t)

	if status != http.StatusOK || echoed != "a3f91c07d2b4e6f8" {
		t.Fatalf("handshake answered %d with %q", status, echoed)
	}
	if !strings.Contains(run.errOut.String(), "handshake") {
		t.Errorf("the handshake was not reported:\n%s", run.errOut)
	}
	if !strings.Contains(run.errOut.String(), "1 handshake") {
		t.Errorf("the summary does not count the handshake:\n%s", run.errOut)
	}
}

func TestAProbeThatIsNotAHandshakeIsRefusedAndNotCounted(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com")
	status, _ := run.probe(t, "hello=world")
	run.stop(t)

	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if strings.Contains(run.errOut.String(), "1 handshake") {
		t.Errorf("a refused probe was counted as a handshake:\n%s", run.errOut)
	}
}

func TestAVerifiedDeliveryIsRenderedOnThePayloadStream(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--exit-after", "1")
	status := run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.wait(t)

	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	if run.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", run.code, run.errOut)
	}
	// The event is the payload, so it goes to stdout and nothing else does. That is what
	// makes the stream pipeable and what --exit-after counts.
	line := run.out.String()
	for _, want := range []string{"email.sent", "9f3b2c14", "alice@example.com"} {
		if !strings.Contains(line, want) {
			t.Errorf("the event line does not carry %q:\n%s", want, line)
		}
	}
	if !strings.Contains(run.errOut.String(), "1 event") {
		t.Errorf("the summary does not count the event:\n%s", run.errOut)
	}
}

func TestARedeliveryIsRecognisedAndNotHandledTwice(t *testing.T) {
	t.Parallel()

	// The platform retries a delivery up to fifteen times under one stable id. A tool that
	// showed each attempt would report fifteen bounces for one bounced message.
	// Deliberately without --exit-after: the run has to outlive the second attempt, and a
	// redelivery must not be what ends it.
	run := start(t, running(), "--public-url", "https://a1b2.example.com")
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	run.deliver(t, "d1", body)
	if status := run.deliver(t, "d1", body); status != http.StatusNoContent {
		t.Fatalf("the redelivery was not acknowledged: %d", status)
	}
	run.stop(t)

	if got := strings.Count(run.out.String(), "email.sent"); got != 1 {
		t.Errorf("the event was shown %d times, want 1:\n%s", got, run.out)
	}
	if !strings.Contains(run.errOut.String(), "duplicate, already handled") {
		t.Errorf("the redelivery was not reported:\n%s", run.errOut)
	}
	if !strings.Contains(run.errOut.String(), "1 duplicate") {
		t.Errorf("the summary does not count the duplicate:\n%s", run.errOut)
	}
}

func TestADeliveryThatDoesNotVerifyIsRefusedAndProducesNoPayload(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com")
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	timestamp := time.Now().UTC().Format(time.RFC3339)
	status := run.post(t, "d1", timestamp, mailkube.Sign("d1", timestamp, []byte(body), "wrong"), body, "/")
	run.stop(t)

	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	// An endpoint URL is public and anyone may post to it. A refused delivery reaching the
	// payload stream would put a stranger's data into whatever consumes it.
	if run.out.Len() != 0 {
		t.Errorf("a refused delivery reached the payload stream:\n%s", run.out)
	}
	if !strings.Contains(run.errOut.String(), "signature invalid") {
		t.Errorf("the refusal was not reported:\n%s", run.errOut)
	}
	if !strings.Contains(run.errOut.String(), "1 rejected") {
		t.Errorf("the summary does not count the refusal:\n%s", run.errOut)
	}
}

func TestAStaleTimestampIsRefusedAndReportedAsAClockProblem(t *testing.T) {
	t.Parallel()

	// The distinction matters: the signature was computed correctly and the two machines
	// disagree about the time, which is a different problem from a wrong secret and has a
	// different fix.
	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--tolerance", "60s")
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	stale := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	status := run.post(t, "d1", stale, mailkube.Sign("d1", stale, []byte(body), secret), body, "/")
	run.stop(t)

	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	reported := run.errOut.String()
	if !strings.Contains(reported, "old") || !strings.Contains(reported, "clock") {
		t.Errorf("the refusal does not point at the clock:\n%s", reported)
	}
}

func TestAFilteredEventIsNeitherShownNorCounted(t *testing.T) {
	t.Parallel()

	// --filter gates the display and the count alike, so a CI step asserting on one event
	// type cannot be satisfied by a different one arriving first.
	run := start(t, running(), "--public-url", "https://a1b2.example.com",
		"--filter", "email.bounced", "--exit-after", "1")
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.deliver(t, "d2", bounced("Mailbox does not exist"))
	run.wait(t)

	if run.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", run.code, run.errOut)
	}
	if strings.Contains(run.out.String(), "email.sent") {
		t.Errorf("a filtered event was shown:\n%s", run.out)
	}
	if !strings.Contains(run.out.String(), "email.bounced") {
		t.Errorf("the matching event was not shown:\n%s", run.out)
	}
	if !strings.Contains(run.errOut.String(), "1 filtered") {
		t.Errorf("the summary does not report what was filtered out:\n%s", run.errOut)
	}
}

func TestABouncedEventCarriesTheReceivingServersVerdict(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--exit-after", "1")
	run.deliver(t, "d1", bounced("Mailbox does not exist"))
	run.wait(t)

	shown := run.out.String()
	if !strings.Contains(shown, "code 550") || !strings.Contains(shown, "Mailbox does not exist") {
		t.Errorf("the bounce does not carry its verdict:\n%s", shown)
	}
}

func TestHostileTextInADeliveryCannotReachTheTerminal(t *testing.T) {
	t.Parallel()

	// A bounce reason is whatever a remote mail server chose to send. This one carries a
	// clipboard write, a hyperlink whose target is not its text, a screen clear and a
	// right-to-left override, which between them can rewrite what this program just reported.
	hostile := "\x1b]52;c;aGk=\x07\x1b]8;;https://evil.example\x07click\x1b]8;;\x07" +
		"\x1b[2J\u202egnp.exe\u200b done"

	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--exit-after", "1")
	run.deliver(t, "d1", bounced(hostile))
	run.wait(t)

	shown := run.out.String()
	for _, forbidden := range []string{"\x1b", "\u202e", "\u200b", "\x07"} {
		if strings.Contains(shown, forbidden) {
			t.Errorf("%q survived into the rendered stream:\n%q", forbidden, shown)
		}
	}
	// The hyperlink's target goes with the sequence that carried it, because the URL was never
	// visible text: it was the escape's payload, and what the reader saw was "click".
	if strings.Contains(shown, "evil.example") {
		t.Errorf("a hyperlink target survived as content:\n%q", shown)
	}
	// Removing the escapes must not remove the reason with them. A bounce whose text was
	// eaten by the defence is a bounce nobody can act on.
	for _, want := range []string{"click", "done", "code 550"} {
		if !strings.Contains(shown, want) {
			t.Errorf("sanitising took the readable text too, %q is missing:\n%q", want, shown)
		}
	}
}

func TestTheMachineStreamCarriesTheDeliveredBody(t *testing.T) {
	t.Parallel()

	opts := running()
	opts.Globals = &settings.Globals{Output: "ndjson"}

	run := start(t, opts, "--public-url", "https://a1b2.example.com", "--exit-after", "1")
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	run.deliver(t, "d1", body)
	run.wait(t)

	var record struct {
		ID       string          `json:"id"`
		Type     string          `json:"type"`
		EmailID  string          `json:"email_id"`
		Verified bool            `json:"verified"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(run.out.String())), &record); err != nil {
		t.Fatalf("the stream is not one JSON document per line: %v\n%s", err, run.out)
	}

	// The full delivery id, never the abbreviated form: a machine format is what a caller
	// deduplicates and correlates on.
	if record.ID != "d1" || record.Type != "email.sent" || !record.Verified {
		t.Errorf("the record does not describe the delivery: %+v", record)
	}
	// The typed fields are this release's reading of the body. The body travels too, so a
	// field this version does not model still reaches whatever consumes the stream.
	var delivered, carried any
	_ = json.Unmarshal([]byte(body), &delivered)
	_ = json.Unmarshal(record.Payload, &carried)
	if !jsonEqual(delivered, carried) {
		t.Errorf("the payload is not what was delivered:\n%s", record.Payload)
	}
}

// jsonEqual compares two decoded JSON values.
func jsonEqual(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func TestAnUnverifiedDeliveryIsBadgedOnEveryLine(t *testing.T) {
	t.Parallel()

	// --skip-verify exists for the moment before a secret is to hand, and every event it
	// accepts says so: the badge is what stops an unverified run from looking like a verified
	// one in a screenshot.
	opts := testsupport.TestOptions{Globals: &settings.Globals{Output: "text"}}
	run := start(t, opts, "--public-url", "https://a1b2.example.com", "--skip-verify", "--exit-after", "1")

	status := run.post(t, "d1", "", "", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"), "/")
	run.wait(t)

	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", status)
	}
	if !strings.Contains(run.out.String(), "UNVERIFIED") {
		t.Errorf("an unverified event is not badged:\n%s", run.out)
	}
	if !strings.Contains(run.errOut.String(), "NOT CHECKED") {
		t.Errorf("the banner does not say verification is off:\n%s", run.errOut)
	}
}

func TestABodyLargerThanTheCapIsRefusedBeforeItIsRead(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--max-body", "64")
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	status := run.deliver(t, "d1", body)
	run.stop(t)

	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", status)
	}
	if run.out.Len() != 0 {
		t.Errorf("an oversized delivery reached the payload stream:\n%s", run.out)
	}
}

func TestADeliveryToAnotherPathIsNotAccepted(t *testing.T) {
	t.Parallel()

	// Registering an endpoint at one path and listening on another is a mistake worth being
	// told about, rather than one that looks like the platform never delivering.
	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--path", "/hooks")
	status := run.post(t, "d1", time.Now().UTC().Format(time.RFC3339), "sha256=x",
		sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"), "/")
	run.stop(t)

	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestExitTimeoutEndsTheRunWithTheDeadlineCode(t *testing.T) {
	t.Parallel()

	// 124 rather than a failure code, because nothing failed: a deadline the caller set was
	// reached, and that is a distinct outcome for the script that set it.
	run := start(t, running(), "--public-url", "https://a1b2.example.com",
		"--exit-after", "1", "--exit-timeout", "200ms")
	run.wait(t)

	if run.code != errs.CodeDeadline {
		t.Fatalf("exit code = %d, want %d", run.code, errs.CodeDeadline)
	}
	if !strings.Contains(run.errOut.String(), "Summary") {
		t.Errorf("the run ended without a summary:\n%s", run.errOut)
	}
}

func TestAnInterruptEndsTheRunAfterReportingWhatItSaw(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com")
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.stop(t)

	// Ctrl-C and a container runtime stopping the process are the same event to a script, and
	// both leave the summary behind rather than dying mid-line.
	if run.code != errs.CodeInterrupt {
		t.Fatalf("exit code = %d, want %d", run.code, errs.CodeInterrupt)
	}
	if !strings.Contains(run.errOut.String(), "1 event") {
		t.Errorf("the summary did not survive the interrupt:\n%s", run.errOut)
	}
}

func TestExitAfterCountsOnlyNewMatchingEvents(t *testing.T) {
	t.Parallel()

	// The three things that must not satisfy it, in one run: a handshake, a filtered event and
	// a redelivery. If any of them counted, the run would end early and the CI recipe in the
	// documentation would pass without the event it claims to assert on.
	//
	// Two are asked for, and exactly five requests arrive of which only two qualify. The
	// deadline is the backstop: if any of the other three were counted the run would end on
	// the fourth request, before the fifth was ever sent.
	run := start(t, running(), "--public-url", "https://a1b2.example.com",
		"--filter", "email.bounced", "--exit-after", "2", "--exit-timeout", "10s")
	run.probe(t, "hub.mode=subscribe&hub.challenge=a3f91c07")
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.deliver(t, "d2", bounced("Mailbox does not exist"))
	run.deliver(t, "d2", bounced("Mailbox does not exist"))
	run.deliver(t, "d3", bounced("Mailbox full"))
	run.wait(t)

	if run.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", run.code, run.errOut)
	}
	if got := strings.Count(run.out.String(), "email.bounced"); got != 2 {
		t.Errorf("the payload stream carries %d events, want 2:\n%s", got, run.out)
	}
}

func TestSummaryOnlyIsStillAStreamWithNoPayload(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com",
		"--print", "summary", "--exit-after", "1")
	run.deliver(t, "d1", sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com"))
	run.wait(t)

	if run.out.Len() != 0 {
		t.Errorf("--print summary still wrote a payload:\n%s", run.out)
	}
	if !strings.Contains(run.errOut.String(), "1 event") {
		t.Errorf("the summary does not count the event:\n%s", run.errOut)
	}
}

func TestRawPrintsTheDeliveredBodyRatherThanAReadingOfIt(t *testing.T) {
	t.Parallel()

	run := start(t, running(), "--public-url", "https://a1b2.example.com",
		"--print", "raw", "--exit-after", "1")
	body := sent("9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77", "alice@example.com")
	run.deliver(t, "d1", body)
	run.wait(t)

	// Not a terminal here, so the bytes travel unaltered: raw bytes to files and pipes,
	// sanitised text to terminals.
	if strings.TrimSpace(run.out.String()) != body {
		t.Errorf("the raw form is not what was delivered:\n%s", run.out)
	}
}

func TestBindingBeyondLoopbackIsAllowedAndSaidOutLoud(t *testing.T) {
	t.Parallel()

	// Refusing would make the published container image unable to run the command it ships,
	// since a published port cannot reach loopback. Saying nothing would let someone expose a
	// listener on a shared network without noticing.
	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--host", "0.0.0.0")
	run.stop(t)

	if !strings.Contains(run.errOut.String(), "reachable from your network") {
		t.Errorf("binding beyond loopback was not reported:\n%s", run.errOut)
	}
}

func TestAnUnknownEventTypeInAFilterIsHonouredAndMentioned(t *testing.T) {
	t.Parallel()

	// The platform may emit a type newer than this binary, and filtering for one is exactly
	// what someone investigating a new event does. Refusing would make the CLI's age the
	// user's problem.
	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--filter", "email.teleported")
	run.stop(t)

	if !strings.Contains(run.errOut.String(), "email.teleported") {
		t.Errorf("an unrecognised filter was not mentioned:\n%s", run.errOut)
	}
}
