package emails_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/emails"
)

// populated is a payload with every field set, so nothing can be omitted by accident.
func populated() emails.Payload {
	return emails.Payload{
		From:            "Acme <hello@acme.com>",
		To:              []string{"alice@example.com"},
		CC:              []string{"cc@example.com"},
		BCC:             []string{"bcc@example.com"},
		ReplyTo:         []string{"reply@acme.com"},
		Subject:         "Welcome",
		HTML:            "<p>hi</p>",
		Text:            "hi",
		TemplateID:      "1f0c1a2b-3c4d-5e6f-7a8b-9c0d1e2f3a4b",
		TemplateVersion: "latest",
		Variables:       map[string]string{"first_name": "Alice"},
		Topic:           "news",
		Tags:            []emails.Tag{{Name: "campaign", Value: "launch"}},
		Headers:         map[string]string{"X-Campaign": "launch"},
		Attachments:     []emails.Attachment{{Filename: "a.txt", Content: "aGk=", ContentType: "text/plain"}},
		ScheduledAt:     "2026-09-01T09:00:00Z",
		BatchID:         "digest-w33",
	}
}

// TestTheSkeletonMatchesWhatTheSDKActuallySends is the parity check the whole design rests on.
//
// Three things have to describe one shape: the payload `--json` accepts, the blank one
// `--generate-skeleton` hands a user to fill in, and the body that goes on the wire. The wire is
// the oracle, and it is read by making the SDK send a request rather than by reading its source,
// because an unexported struct tag is not something this repository can assert against and a
// hand-copied list is a list that drifts.
func TestTheSkeletonMatchesWhatTheSDKActuallySends(t *testing.T) {
	t.Parallel()

	var body map[string]json.RawMessage
	var idempotency string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idempotency = r.Header.Get("Idempotency-Key")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("the request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"9f3b2c14","message_id":"<x@msg>"}`))
	}))
	defer server.Close()

	client, err := mailkube.New(mailkube.WithAPIKey("mk_test"), mailkube.WithBaseURL(server.URL+"/"))
	if err != nil {
		t.Fatalf("building the client: %v", err)
	}

	params, err := populated().Params("k1")
	if err != nil {
		t.Fatalf("converting the payload: %v", err)
	}
	if _, err := client.Emails.Send(context.Background(), params); err != nil {
		t.Fatalf("send: %v", err)
	}

	wire := make([]string, 0, len(body))
	for key := range body {
		wire = append(wire, key)
	}
	skeleton := emails.SkeletonKeys()
	sort.Strings(wire)
	sort.Strings(skeleton)

	if len(wire) != len(skeleton) {
		t.Fatalf("the wire carries %v; the skeleton offers %v", wire, skeleton)
	}
	for i := range wire {
		if wire[i] != skeleton[i] {
			t.Errorf("key %d: the wire says %q, the skeleton says %q", i, wire[i], skeleton[i])
		}
	}

	// The idempotency key travels as a header, which is why the skeleton deliberately has no
	// field for it: one written into a payload file would be silently ignored.
	if idempotency != "k1" {
		t.Errorf("Idempotency-Key header = %q, want k1", idempotency)
	}
	if _, inBody := body["idempotency_key"]; inBody {
		t.Error("the idempotency key was sent in the body, where the server does not read it")
	}
}

func TestASchedulingTimeMustCarryAnOffset(t *testing.T) {
	t.Parallel()

	p := populated()
	p.ScheduledAt = "2026-09-01 09:00"

	if _, err := p.Params(""); err == nil {
		t.Error("a scheduling time with no offset was accepted, so it names a different instant per zone")
	}
}

func TestAnEmptySchedulingTimeIsAnImmediateSend(t *testing.T) {
	t.Parallel()

	p := populated()
	p.ScheduledAt = ""

	params, err := p.Params("")
	if err != nil {
		t.Fatalf("converting: %v", err)
	}
	if !params.ScheduledAt.IsZero() {
		t.Error("an absent scheduling time became a real one")
	}
}

func TestElidingLeavesTheOriginalPayloadIntact(t *testing.T) {
	t.Parallel()

	p := populated()
	elided := p.Elided()

	encoded, err := json.Marshal(elided)
	if err != nil {
		t.Fatalf("marshalling the elided payload: %v", err)
	}
	if !strings.Contains(string(encoded), "base64 omitted") {
		t.Errorf("the elided payload still carries content: %s", encoded)
	}

	// The elision is a rendering, not a mutation: the payload that gets sent afterwards must
	// still carry the file.
	if p.Attachments[0].Content != "aGk=" {
		t.Error("eliding a payload for display destroyed the content that was about to be sent")
	}
	params, err := p.Params("")
	if err != nil {
		t.Fatalf("converting: %v", err)
	}
	if string(params.Attachments[0].Content) != "hi" {
		t.Errorf("attachment content = %q, want the decoded bytes", params.Attachments[0].Content)
	}
}

func TestTheEncodedSizeIsWhatGoesOnTheWire(t *testing.T) {
	t.Parallel()

	p := emails.Payload{Attachments: []emails.Attachment{
		{Filename: "a", Content: "0123456789"},
		{Filename: "b", Content: "0123456789"},
	}}
	if got := p.EncodedSize(); got != 20 {
		t.Errorf("EncodedSize() = %d, want 20", got)
	}
}

func TestADerivedKeyIsStableAcrossEquivalentPayloads(t *testing.T) {
	t.Parallel()

	first, err := emails.DeriveKey(populated())
	if err != nil {
		t.Fatalf("deriving: %v", err)
	}
	second, err := emails.DeriveKey(populated())
	if err != nil {
		t.Fatalf("deriving: %v", err)
	}
	if first != second {
		t.Error("the same payload derived two different keys")
	}

	changed := populated()
	changed.Text += "."
	third, err := emails.DeriveKey(changed)
	if err != nil {
		t.Fatalf("deriving: %v", err)
	}
	// One character of difference has to produce a different key, or a genuinely different
	// message would be swallowed as a replay of the previous one.
	if third == first {
		t.Error("a changed body derived the same key")
	}
}
