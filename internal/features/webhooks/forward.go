package webhooks

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// forwardHeaders are the headers a forward carries through unchanged.
//
// The signature is computed over the raw body together with the id and the timestamp, so all
// three have to arrive intact or the downstream app's own verification fails. That verification
// passing byte for byte is the whole point of forwarding rather than re-emitting: the local app
// runs exactly the code it will run in production, against exactly the bytes the platform sent.
func forwardHeaders() []string {
	return []string{headerDeliveryID, headerDeliveryTimestamp, headerDeliverySignature}
}

// outcome is what became of one forward.
type outcome struct {
	// status is the response code, zero when the request never completed.
	status int
	// elapsed is how long the target took.
	elapsed time.Duration
	// err is why it failed, nil on any response at all.
	err error
}

// ok reports whether the target accepted it.
func (o outcome) ok() bool { return o.err == nil && o.status >= 200 && o.status < 300 }

// describe renders the outcome as the line under the event it belongs to.
//
// The target is named on every line, including the successful ones. A forward stream where only
// the failures say where they went is one where the first failure raises the question the
// successes should already have answered.
func (o outcome) describe(target string) string {
	if o.err != nil {
		// The error text is this program's and the standard library's, not a server's, but
		// it embeds the URL the user supplied, so it is sanitised like anything else that
		// carries a value from outside.
		return "→ " + target + " did not answer: " + output.Sanitize(o.err.Error())
	}

	line := "→ " + target + " " + strconv.Itoa(o.status)
	if o.elapsed >= time.Millisecond {
		line += " (" + o.elapsed.Round(time.Millisecond).String() + ")"
	}
	return line
}

// forwarder re-posts a delivery to a local application.
type forwarder struct {
	// target is where to post.
	target string
	// client is the HTTP client, with the per-request timeout already set.
	client *http.Client
	// clock measures how long the target took, through the same seam as everything else, so
	// that a fixed clock renders no duration at all rather than a different one each run.
	clock clock.Clock
}

// newForwarder builds a forwarder with a bounded per-request timeout.
func newForwarder(target string, wait time.Duration, c clock.Clock) *forwarder {
	return &forwarder{target: target, client: &http.Client{Timeout: wait}, clock: c}
}

// send re-posts one delivery and reports what happened.
//
// The context is the request's own rather than the listener's, so a forward already in flight when
// a stop arrives still gets its bounded moment to finish rather than being cut off mid-write.
func (f *forwarder) send(ctx context.Context, body []byte, headers http.Header) outcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.target, bytes.NewReader(body))
	if err != nil {
		return outcome{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	for _, name := range forwardHeaders() {
		if value := headers.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}

	started := f.clock.Now()
	resp, err := f.client.Do(req)
	elapsed := f.clock.Now().Sub(started)
	if err != nil {
		return outcome{elapsed: elapsed, err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// The response body is drained and dropped. Reading it lets the connection be reused;
	// showing it would put a local application's error page into a webhook stream, where it
	// would read as something the platform said.
	_, _ = io.Copy(io.Discard, resp.Body)
	return outcome{status: resp.StatusCode, elapsed: elapsed}
}
