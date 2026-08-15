package webhooks

import (
	"bytes"
	"encoding/json"
	"os"
	"sync"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// recordMode is what a capture file is created with.
//
// A capture holds recipient addresses and subjects, which is personal data belonging to whoever
// was sent the mail rather than to the developer debugging it. 0600 is the same rule the config
// file follows, and for the same reason.
const recordMode = 0o600

// Capture is one recorded delivery, as a line of the .jsonl file.
//
// It carries the signature headers alongside the body rather than the body alone, because a
// capture is only replayable if it can still be verified: `webhooks verify --tolerance 0` needs
// the id, the timestamp and the signature, and a file holding just payloads would have thrown
// away the three fields that prove where they came from.
type Capture struct {
	// ID is the delivery id from X-Webhook-Id.
	ID string `json:"id"`
	// Timestamp is the signed delivery timestamp from X-Webhook-Ts.
	Timestamp string `json:"timestamp"`
	// Signature is the X-Webhook-Sig value, in full.
	Signature string `json:"signature"`
	// ReceivedAt is when this machine took delivery.
	ReceivedAt string `json:"received_at"`
	// Verified reports whether the signature was checked when it arrived.
	Verified bool `json:"verified"`
	// Body is the delivered body, as the exact bytes that arrived.
	//
	// A string rather than embedded JSON, and the difference is the whole usefulness of the
	// file. A signature is computed over exact bytes, and embedding the payload as a value
	// would have Go compact it on the way in and any reader re-encode it on the way out;
	// either is enough to make a genuine delivery look forged. As a string it survives both,
	// so `jq -r .body` hands back something `webhooks verify` still accepts.
	Body string `json:"body"`
}

// recorder appends captures to a file.
//
// Append rather than truncate: restarting a listener is something people do while chasing a
// problem, and a capture destroyed by the restart is the one they were about to read. Flushed per
// line and fsynced on shutdown, because the whole point of a capture is that it survives whatever
// happened next.
type recorder struct {
	mu   sync.Mutex
	file *os.File
}

// newRecorder opens the capture file, creating it if it does not exist.
func newRecorder(path string) (*recorder, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, recordMode)
	if err != nil {
		return nil, errs.Configf("cannot open %s for recording: %v", path, err)
	}
	return &recorder{file: file}, nil
}

// write appends one capture.
//
// The body goes in verbatim. This is a file, not a terminal, so the invariant applies in the
// direction that matters here: raw bytes to files and pipes, sanitised text to terminals. A
// consumer of this file is going to verify a signature over those bytes, and a sanitised copy
// would not verify.
func (r *recorder) write(c Capture) error {
	// Encoded into a buffer and written once, so a line reaches the file whole: two deliveries
	// finishing together must not interleave halfway through a JSON document.
	//
	// HTML escaping is off for the same reason the rest of this CLI turns it off: an ampersand
	// in a subject becoming & is valid JSON and unreadable to a person comparing a capture
	// against what they sent. It changes nothing about the round trip, since the body is a
	// string either way.
	var line bytes.Buffer
	enc := json.NewEncoder(&line)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.file.Write(line.Bytes())
	return err
}

// close fsyncs and releases the file.
//
// Sync before Close rather than trusting the close: a listener is usually ended by a signal, and
// the buffered write that never reached the disk is exactly the last event, which is the one
// worth having.
func (r *recorder) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.file.Sync()
	_ = r.file.Close()
}

// capture builds the record for one delivery.
func capture(event *mailkube.Event, receivedAt, signature string, verified bool) Capture {
	return Capture{
		ID:         event.ID,
		Timestamp:  event.Timestamp,
		Signature:  signature,
		ReceivedAt: receivedAt,
		Verified:   verified,
		Body:       string(event.Raw),
	}
}
