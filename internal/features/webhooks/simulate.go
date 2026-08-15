package webhooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// fixtures holds one sample payload per event type.
//
// Committed rather than generated at runtime, so the shape a developer tests against is a shape
// someone read and agreed to, and so a change to it is a reviewable diff. Embedded rather than
// shipped beside the binary, because `go install` and the container image both deliver a single
// file and neither would carry a directory.
//
//go:embed events/*.json
var fixtures embed.FS

// deliveryTimestampLayout is how the platform writes X-Webhook-Ts.
//
// Microseconds and a numeric "+00:00" offset rather than "Z", which is worth copying exactly: the
// timestamp is part of the signed input, and a receiver with a strict Z-only parser fails on the
// real thing. A simulation that sent the easier spelling would hide that failure until production.
const deliveryTimestampLayout = "2006-01-02T15:04:05.000000-07:00"

// simulateCmd builds `webhooks simulate`.
func (f *Feature) simulateCmd(deps *feature.Deps) *cobra.Command {
	var target, eventType, file, secret string
	var force bool

	cmd := &cobra.Command{
		Use:   "simulate",
		Short: "Post a synthetic event to a local endpoint",
		Long: "Post a synthetic event to a local endpoint.\n\n" +
			"Mailkube has no sandbox and no reserved test address, so every real send costs quota\n" +
			"and moves your sending reputation. This is the way to exercise a handler, a forward\n" +
			"target or a rendering path for nothing, with the signature left on.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return f.simulate(c.Context(), deps, simulation{
				target: target, eventType: eventType, file: file, secret: secret, force: force,
			})
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&target, "url", "", "endpoint to post to; loopback unless --force")
	fs.StringVar(&eventType, "event", "", "event type to send; run without it to see the list")
	fs.StringVar(&file, "file", "", "payload to send instead of the sample one (@file or @-)")
	fs.StringVar(&secret, "secret", "", "sign with this secret; MAILKUBE_WEBHOOK_SECRET is read too")
	fs.BoolVar(&force, "force", false, "allow a target that is not on this machine")
	return cmd
}

// simulation is one synthetic delivery, as asked for.
type simulation struct {
	target    string
	eventType string
	file      string
	secret    string
	force     bool
}

// SimulationView is what was sent and what the endpoint said.
type SimulationView struct {
	// ID is the delivery id that was generated, in full.
	ID string `json:"id"`
	// Type is the event type sent.
	Type string `json:"type"`
	// Timestamp is the delivery timestamp, in the platform's own spelling.
	Timestamp string `json:"timestamp"`
	// Signed reports whether a signature was attached.
	Signed bool `json:"signed"`
	// URL is where it was posted.
	URL string `json:"url"`
	// Status is what the endpoint answered.
	Status int `json:"status"`
}

// RenderText implements output.TextRenderer.
func (v SimulationView) RenderText(caps output.Caps) []string {
	table := output.Table{Rows: [][]string{
		{"event", v.Type},
		{"id", v.ID},
		{"signed", signedWord(v.Signed)},
		{"status", http.StatusText(v.Status)},
	}}

	lines := []string{caps.Glyphs.OK + " Delivered to " + v.URL}
	for _, line := range table.Lines() {
		lines = append(lines, "  "+line)
	}
	return lines
}

// signedWord says whether the delivery carried a signature, in the terms the receiver sees.
func signedWord(signed bool) string {
	if signed {
		return "yes"
	}
	return "no, so the endpoint should refuse it"
}

// simulate posts one synthetic event.
func (f *Feature) simulate(ctx context.Context, deps *feature.Deps, s simulation) error {
	target, err := simulationTarget(s)
	if err != nil {
		return err
	}
	body, eventType, err := f.payload(deps, s)
	if err != nil {
		return err
	}

	id, err := deliveryID()
	if err != nil {
		return err
	}
	timestamp := deps.Clock.Now().UTC().Format(deliveryTimestampLayout)

	secret := secretFrom(deps, s.secret)

	status, err := f.post(ctx, target, id, timestamp, secret, body)
	if err != nil {
		return err
	}

	view := SimulationView{
		ID: id, Type: eventType, Timestamp: timestamp,
		Signed: secret != "", URL: target, Status: status,
	}
	if err := deps.Emit(view); err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return errs.Newf(errs.CodeServer, "%s answered %d", target, status)
	}
	return nil
}

// simulationTarget checks where the event is being posted.
func simulationTarget(s simulation) (string, error) {
	value := strings.TrimSpace(s.target)
	if value == "" {
		return "", errs.Usagef("--url is required: it is the endpoint to post to, usually your own listener")
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errs.Usagef("--url %q is not an http or https URL", value)
	}
	// The same guard as --forward, for the same reason: this command posts a signed payload,
	// and pointing it at a stranger turns a development tool into something else.
	if !s.force && !loopback(parsed.Hostname()) {
		return "", errs.Usagef(
			"--url %q is not on this machine. Pass --force if you meant it.", value)
	}
	return value, nil
}

// payload returns the body to send and the event type it describes.
func (f *Feature) payload(deps *feature.Deps, s simulation) (body []byte, eventType string, err error) {
	if s.file != "" {
		// A supplied payload is sent as given. Its own "type" is reported rather than the
		// flag's, because what the endpoint receives is the file, and reporting anything
		// else would describe a delivery that did not happen.
		content, err := input.NewReader(deps.IO.In).Resolve(s.file)
		if err != nil {
			return nil, "", err
		}
		return []byte(content), typeOf([]byte(content)), nil
	}

	name := strings.TrimSpace(s.eventType)
	if name == "" {
		return nil, "", errs.Usagef("--event is required. Available:\n%s", strings.Join(indented(), "\n"))
	}

	sample, err := fixtures.ReadFile("events/" + name + ".json")
	if err != nil {
		return nil, "", errs.Usagef("no sample payload for %q. Available:\n%s",
			output.Sanitize(name), strings.Join(indented(), "\n"))
	}
	return sample, name, nil
}

// typeOf reads the event type out of a supplied payload, without failing on one that has none.
func typeOf(body []byte) string {
	event, err := mailkube.ParseEvent(body)
	if err != nil {
		return "unknown"
	}
	return event.Type
}

// available lists the event types with a sample payload.
func available() []string {
	entries, err := fixtures.ReadDir("events")
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
	}
	sort.Strings(names)
	return names
}

// indented renders the list for an error message.
func indented() []string {
	names := available()
	for i, name := range names {
		names[i] = "  " + name
	}
	return names
}

// post sends the delivery the way the platform would.
func (f *Feature) post(
	ctx context.Context, target, id, timestamp, secret string, body []byte,
) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, errs.Usagef("cannot post to %s: %v", target, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerDeliveryID, id)
	req.Header.Set(headerDeliveryTimestamp, timestamp)
	if secret != "" {
		// Through the SDK's own signing, so a simulated delivery is signed by exactly the
		// code that verifies it. A second implementation of the construction here would
		// eventually disagree with the first, and the test would pass while production did
		// not.
		req.Header.Set(headerDeliverySignature, mailkube.Sign(id, timestamp, body, secret))
	}

	client := &http.Client{Timeout: simulateTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, errs.Newf(errs.CodeNetwork, "%s did not answer: %v", target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

// simulateTimeout bounds the one request this command makes.
const simulateTimeout = 30 * time.Second

// deliveryID generates a delivery id in the shape the platform sends.
//
// A version-4 UUID, built here rather than pulled in as a dependency: sixteen random bytes with
// six of them fixed is the whole specification, and the alternative is a module in the go.mod of
// a tool whose dependency list is short on purpose.
func deliveryID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errs.Newf(errs.CodeInternal, "cannot generate a delivery id: %v", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80

	hexed := hex.EncodeToString(raw[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:], nil
}
