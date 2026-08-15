package webhooks

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// verifyCmd builds `webhooks verify`.
func (f *Feature) verifyCmd(deps *feature.Deps) *cobra.Command {
	var body, id, timestamp, signature, secret string
	var tolerance time.Duration

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check a webhook signature offline",
		Long: "Check a webhook signature offline.\n\n" +
			"Nothing is sent and nothing is contacted: this is the same computation a receiver\n" +
			"performs, run against a payload you already have. Pass --tolerance 0 to check a\n" +
			"capture from yesterday, where the timestamp is legitimately old.\n\n" +
			"To check the first delivery in a `--record` capture:\n\n" +
			"  head -1 events.jsonl > d.json\n" +
			"  jq -r .body d.json > body.json\n" +
			"  mailkube webhooks verify --body @body.json --tolerance 0 \\\n" +
			"    --id \"$(jq -r .id d.json)\" --ts \"$(jq -r .timestamp d.json)\" \\\n" +
			"    --sig \"$(jq -r .signature d.json)\"",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return f.verify(deps, verification{
				body: body, id: id, timestamp: timestamp,
				signature: signature, secret: secret, tolerance: tolerance,
			})
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&body, "body", "", "the raw payload, as @file or @- (never re-encoded)")
	fs.StringVar(&id, "id", "", "the X-Webhook-Id value")
	fs.StringVar(&timestamp, "ts", "", "the X-Webhook-Ts value")
	fs.StringVar(&signature, "sig", "", "the X-Webhook-Sig value")
	fs.StringVar(&secret, "secret", "", "the endpoint signing secret; MAILKUBE_WEBHOOK_SECRET is read too")
	fs.DurationVar(&tolerance, "tolerance", defaultTolerance, "accepted age; 0 checks the signature only")
	return cmd
}

// verification is one offline check, as asked for.
type verification struct {
	body      string
	id        string
	timestamp string
	signature string
	secret    string
	tolerance time.Duration
}

// VerificationView is the verdict on one payload.
type VerificationView struct {
	// Valid is always true in a rendered result: a failure is an error, not a document.
	Valid bool `json:"valid"`
	// ID is the delivery id the signature was computed over.
	ID string `json:"id"`
	// Type is the event type the payload turned out to be.
	Type string `json:"type"`
	// EmailID is the message it concerns, when it concerns one.
	EmailID string `json:"email_id,omitempty"`
	// Timestamp is the signed delivery timestamp.
	Timestamp string `json:"timestamp"`
	// Age is how old the delivery was when it was checked.
	Age string `json:"age"`
}

// RenderText implements output.TextRenderer.
func (v VerificationView) RenderText(caps output.Caps) []string {
	table := output.Table{Rows: [][]string{
		{"event", output.Sanitize(v.Type)},
		{"id", output.Sanitize(v.ID)},
		{"age", v.Age},
	}}

	lines := []string{caps.Glyphs.OK + " Signature valid"}
	for _, line := range table.Lines() {
		lines = append(lines, "  "+line)
	}
	return lines
}

// verify checks one payload and reports the verdict.
//
// The payload is verified as the bytes it arrived as. That is why --body takes a file or stdin
// and is never re-encoded on the way through: a signature is computed over exact bytes, and a
// round trip through a JSON decoder reorders keys and drops whitespace, which is enough to make a
// valid delivery look forged.
func (f *Feature) verify(deps *feature.Deps, v verification) error {
	secret, err := v.settle(deps)
	if err != nil {
		return err
	}

	payload, err := input.NewReader(deps.IO.In).Resolve(v.body)
	if err != nil {
		return err
	}

	event, err := mailkube.Verify([]byte(payload), v.headers(), secret, v.window())
	if err != nil {
		return err
	}
	return deps.Emit(verified(event, v, deps.Clock.Now()))
}

// settle checks that everything the computation needs is present, and finds the secret.
func (v verification) settle(deps *feature.Deps) (string, error) {
	if v.body == "" {
		return "", errs.Usagef("--body is required: pass @file, or @- to read the payload from stdin")
	}
	// Named in a fixed order, because a map would report whichever one Go happened to walk
	// first and a user fixing three missing flags would be told about them in a different
	// order each run.
	required := []struct{ name, value string }{
		{"--id", v.id}, {"--ts", v.timestamp}, {"--sig", v.signature},
	}
	for _, flag := range required {
		if strings.TrimSpace(flag.value) == "" {
			return "", errs.Usagef(
				"%s is required: it is part of what the signature was computed over", flag.name)
		}
	}

	secret := secretFrom(deps, v.secret)
	if secret == "" {
		return "", errs.Configf("no signing secret. Pass --secret or set %s.", settings.EnvWebhookSecret)
	}
	return secret, nil
}

// headers presents the three flags as the lookup the SDK verifies against.
func (v verification) headers() mailkube.HeaderGetter {
	return mailkube.HeaderFunc(func(name string) string {
		switch name {
		case headerDeliveryID:
			return v.id
		case headerDeliveryTimestamp:
			return v.timestamp
		case headerDeliverySignature:
			return v.signature
		default:
			return ""
		}
	})
}

// window is the freshness tolerance to check against.
//
// A tolerance of zero means "do not check freshness", which is the whole point of an offline
// verifier: the common use is a capture from a previous run, and a check that failed on every
// capture older than five minutes would be a verifier nobody could use for the thing it is for.
// The SDK reads zero as "use the default", so the skip is expressed as a window nothing can fall
// outside rather than as a flag it does not have.
func (v verification) window() time.Duration {
	if v.tolerance <= 0 {
		return neverStale
	}
	return v.tolerance
}

// neverStale is the freshness window used when the caller asked for no freshness check.
//
// A hundred years rather than a special case threaded through the SDK call: the check still runs,
// and nothing a person can hold in a file falls outside it.
const neverStale = 100 * 365 * 24 * time.Hour

// verified builds the view from a payload that checked out.
func verified(event *mailkube.Event, v verification, now time.Time) VerificationView {
	view := VerificationView{
		Valid:     true,
		ID:        event.ID,
		Type:      event.Type,
		EmailID:   detailOf(event.Data).emailID,
		Timestamp: event.Timestamp,
		Age:       "unknown",
	}

	if sent, err := time.Parse(time.RFC3339, v.timestamp); err == nil {
		view.Age = elapsed(now.Sub(sent))
	}
	return view
}
