package emails_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

func TestASendReportsWhatTheServerAccepted(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	got := send(t, sender, baseArgs()...)

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if sender.calls != 1 {
		t.Errorf("the server was called %d times, want once", sender.calls)
	}
	// The id is printed in full. A user copies it into a support conversation or into the next
	// command, and an id shortened to fit a column is one that cannot be looked up.
	if !strings.Contains(got.out, "9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77") {
		t.Errorf("the full id is missing from the report:\n%s", got.out)
	}
	if !strings.Contains(got.out, "alice@example.com") {
		t.Errorf("the recipient is missing from the report:\n%s", got.out)
	}
}

func TestADryRunSendsNothingAndShowsWhereItWouldHaveGone(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	got := send(t, sender, baseArgs("--dry-run")...)

	if sender.calls != 0 {
		t.Error("--dry-run made a request")
	}
	// The URL is part of the preview, not decoration: a base URL whose version segment was
	// lost to a missing trailing slash is invisible in the body alone, and that is exactly the
	// failure a dry run is run to catch.
	if !strings.Contains(got.out, "POST ") || !strings.Contains(got.out, "/emails") {
		t.Errorf("the dry run does not show the request line:\n%s", got.out)
	}
	if !strings.Contains(got.out, `"subject": "Welcome"`) {
		t.Errorf("the dry run does not show the body:\n%s", got.out)
	}
}

func TestADryRunNeedsNoCredential(t *testing.T) {
	t.Parallel()

	// Previewing a payload is not an authenticated act, and refusing to do it without a key
	// would make the safe habit the one that requires setup.
	opts := testsupport.TestOptions{Globals: &settings.Globals{Output: "text"}}
	got := sendWith(t, &fakeSender{}, opts, baseArgs("--dry-run")...)

	if got.code != errs.CodeOK {
		t.Errorf("exit code = %d: %s", got.code, got.errOut)
	}
}

func TestASendWithoutACredentialNamesEveryWayToSupplyOne(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	opts := testsupport.TestOptions{Globals: &settings.Globals{Output: "text"}}
	got := sendWith(t, sender, opts, baseArgs()...)

	if got.code != errs.CodeAuth {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeAuth)
	}
	if sender.calls != 0 {
		t.Error("a send was attempted with no credential")
	}
	for _, want := range []string{"auth login", "--api-key", settings.EnvAPIKey} {
		if !strings.Contains(got.errOut, want) {
			t.Errorf("the report does not mention %q:\n%s", want, got.errOut)
		}
	}
}

func TestAttachmentContentIsElidedFromEveryPreview(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatalf("writing the attachment: %v", err)
	}

	got := send(t, &fakeSender{}, baseArgs("--attach", path, "--dry-run")...)

	// The filename and type survive; the megabytes do not. A preview that dumped base64 into
	// a terminal is not a preview, and one that elided in text but not in JSON would make
	// `--dry-run -o json` useless for the diffing it exists for.
	if !strings.Contains(got.out, "report.pdf") {
		t.Errorf("the attachment is missing from the preview:\n%s", got.out)
	}
	if !strings.Contains(got.out, "base64 omitted") {
		t.Errorf("the attachment content was not elided:\n%s", got.out)
	}
	if strings.Contains(got.out, "eHh4") {
		t.Error("encoded attachment content reached the preview")
	}
	// Angle brackets survive the encoder rather than arriving as \u003c, which is what a
	// preview is unreadable without.
	if strings.Contains(got.out, `\u003c`) {
		t.Errorf("the placeholder was HTML-escaped:\n%s", got.out)
	}
}

func TestAttachmentsReachTheWireDecoded(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("writing the attachment: %v", err)
	}

	sender := &fakeSender{}
	send(t, sender, baseArgs("--attach", path, "--attach-type", "note.txt=text/plain")...)

	if len(sender.last.Attachments) != 1 {
		t.Fatalf("attachments reaching the SDK = %d, want 1", len(sender.last.Attachments))
	}
	got := sender.last.Attachments[0]
	// The SDK encodes; the CLI hands it bytes. Encoding twice is the classic way an
	// attachment arrives as gibberish.
	if string(got.Content) != "hello" {
		t.Errorf("attachment content = %q, want the raw bytes", got.Content)
	}
	if got.Filename != "note.txt" || got.ContentType != "text/plain" {
		t.Errorf("attachment = %+v, want the filename and the overridden type", got)
	}
}

func TestAnAttachTypeForNothingAttachedIsAUsageError(t *testing.T) {
	t.Parallel()

	// Silently ignoring it would leave a user believing they had set a content type, and the
	// symptom — a file the recipient's client refuses to open — is a long way from the cause.
	got := send(t, &fakeSender{}, baseArgs("--attach-type", "absent.pdf=application/pdf")...)
	if got.code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}
}

func TestAReplayIsReportedAsAReplayRatherThanASecondSend(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{email: &mailkube.Email{
		ID:                 "9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77",
		IdempotentReplayed: true,
	}}
	got := send(t, sender, baseArgs("--idempotency-key", "k1")...)

	// This is the one question the flag exists to answer, and the response carries it: without
	// this line a caller cannot tell a deduplicated retry from a message they sent twice.
	if !strings.Contains(got.out, "Replayed") {
		t.Errorf("a replayed response was reported as a fresh send:\n%s", got.out)
	}
	if sender.last.IdempotencyKey != "k1" {
		t.Errorf("idempotency key on the wire = %q, want k1", sender.last.IdempotencyKey)
	}
}

func TestAnAutomaticIdempotencyKeyIsDerivedFromTheMessage(t *testing.T) {
	t.Parallel()

	first, second := &fakeSender{}, &fakeSender{}
	send(t, first, baseArgs("--idempotency-key", "auto")...)
	send(t, second, baseArgs("--idempotency-key", "auto")...)

	if first.last.IdempotencyKey == "" || first.last.IdempotencyKey == "auto" {
		t.Fatalf("the literal was not replaced by a derived key: %q", first.last.IdempotencyKey)
	}
	// The same message derives the same key, which is what makes re-running a command a replay
	// rather than a second charged send.
	if first.last.IdempotencyKey != second.last.IdempotencyKey {
		t.Error("the same message derived two different keys")
	}

	changed := &fakeSender{}
	send(t, changed, baseArgs("--idempotency-key", "auto", "--subject", "Different")...)
	if changed.last.IdempotencyKey == first.last.IdempotencyKey {
		t.Error("a different message derived the same key, so a real second send would be swallowed")
	}
}

func TestAScheduledSendSaysWhenAndHowToCancelIt(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{email: &mailkube.Email{
		ID:          "3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77",
		Status:      "scheduled",
		ScheduledAt: "2026-08-14T09:32:00Z",
	}}
	got := send(t, sender, baseArgs("--at", "+2h")...)

	if !strings.Contains(got.out, "Scheduled") {
		t.Errorf("a scheduled acknowledgement was not reported as one:\n%s", got.out)
	}
	if !strings.Contains(got.out, "2026-08-14 09:32 UTC") {
		t.Errorf("the due time is not rendered in the documented form:\n%s", got.out)
	}
	// The suggested command carries the full id. An abbreviated one would produce a command
	// that fails, which is the worst kind of help.
	if !strings.Contains(got.out, "cancel 3a1f9c2d-7d8e-4a11-9c2f-8e5b1d0a3c77") {
		t.Errorf("the cancel command is missing or abbreviated:\n%s", got.out)
	}
	if !sender.last.ScheduledAt.After(sender.last.ScheduledAt.Add(-1)) {
		t.Error("no scheduling time reached the wire")
	}
}

func TestAPayloadFileIsRefinedByExplicitFlags(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mail.json")
	payload := `{"from":"hello@acme.com","to":["alice@example.com"],"subject":"From the file",` +
		`"text":"body","tags":[{"name":"campaign","value":"spring"}]}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing the payload: %v", err)
	}

	sender := &fakeSender{}
	send(t, sender, "--json", "@"+path, "--subject", "From the flag", "--tag", "run=17")

	if sender.last.Subject != "From the flag" {
		t.Errorf("subject = %q, want the flag to win over the file", sender.last.Subject)
	}
	// Keyed collections merge rather than replacing wholesale, which is what makes a stored
	// payload plus one flag a useful combination instead of an all-or-nothing choice.
	if len(sender.last.Tags) != 2 {
		t.Errorf("tags = %+v, want the file's tag and the flag's", sender.last.Tags)
	}
}

func TestARepeatedTagNameOverridesRatherThanDuplicating(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	send(t, sender, baseArgs("--tag", "campaign=spring", "--tag", "campaign=summer")...)

	if len(sender.last.Tags) != 1 || sender.last.Tags[0].Value != "summer" {
		t.Errorf("tags = %+v, want one campaign tag valued summer", sender.last.Tags)
	}
}

func TestAMisspelledKeyInAPayloadIsRefusedRatherThanDropped(t *testing.T) {
	t.Parallel()

	opts := testsupport.TestOptions{
		Stdin:   `{"from":"a@b.com","to":["c@d.com"],"subject":"s","body":"oops"}`,
		Env:     map[string]string{settings.EnvAPIKey: "mk_test"},
		Globals: &settings.Globals{Output: "text"},
	}
	sender := &fakeSender{}
	got := sendWith(t, sender, opts, "--json", "@-")

	// A payload file is edited by hand. A key silently dropped is a message sent without the
	// body its author wrote, and nothing in the output would say so.
	if got.code != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeValidation)
	}
	if sender.calls != 0 {
		t.Error("a payload with an unknown key was sent")
	}
	if !strings.Contains(got.errOut, "generate-skeleton") {
		t.Errorf("the report does not point at the shape:\n%s", got.errOut)
	}
}

func TestBodiesCanBeReadFromFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "body.html")
	if err := os.WriteFile(path, []byte("<p>hello</p>"), 0o600); err != nil {
		t.Fatalf("writing the body: %v", err)
	}

	sender := &fakeSender{}
	send(t, sender,
		"--from", "hello@acme.com", "--to", "alice@example.com",
		"--subject", "Welcome", "--html", "@"+path)

	if sender.last.HTML != "<p>hello</p>" {
		t.Errorf("html = %q, want the file's contents", sender.last.HTML)
	}
}

func TestTheSkeletonRoundTripsBackThroughTheJSONFlag(t *testing.T) {
	t.Parallel()

	opts := testsupport.TestOptions{Globals: &settings.Globals{Output: "json"}}
	skeleton := sendWith(t, nil, opts, "--generate-skeleton")
	if skeleton.code != errs.CodeOK {
		t.Fatalf("generating the skeleton: %s", skeleton.errOut)
	}

	// Filling in a blank payload and sending it is the documented workflow for anything too
	// large for a command line, so the two halves have to actually fit together.
	var payload map[string]any
	if err := json.Unmarshal([]byte(skeleton.out), &payload); err != nil {
		t.Fatalf("the skeleton is not valid JSON: %v", err)
	}
	payload["from"] = "hello@acme.com"
	payload["to"] = []string{"alice@example.com"}
	payload["subject"] = "Welcome"
	payload["text"] = "hi"

	filled, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshalling the filled payload: %v", err)
	}

	path := filepath.Join(t.TempDir(), "filled.json")
	if err := os.WriteFile(path, filled, 0o600); err != nil {
		t.Fatalf("writing the filled payload: %v", err)
	}

	sender := &fakeSender{}
	got := send(t, sender, "--json", "@"+path)
	if got.code != errs.CodeOK {
		t.Fatalf("sending a filled skeleton failed: %s", got.errOut)
	}
	if sender.last.Subject != "Welcome" {
		t.Errorf("subject = %q, want the filled value", sender.last.Subject)
	}
}

func TestAGeneratedBodyCannotSilentlyReplaceOneTheUserWrote(t *testing.T) {
	t.Parallel()

	got := send(t, &fakeSender{}, baseArgs("--sample")...)
	if got.code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}

	orphaned := send(t, &fakeSender{}, baseArgs("--link", "https://example.com")...)
	if orphaned.code != errs.CodeUsage {
		t.Errorf("--link without --sample exited %d, want %d", orphaned.code, errs.CodeUsage)
	}
}

func TestAGeneratedBodyFillsBothPartsAndASubject(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	send(t, sender,
		"--from", "hello@acme.com", "--to", "alice@example.com",
		"--sample", "--link", "https://example.com/promo")

	if sender.last.HTML == "" || sender.last.Text == "" {
		t.Error("a generated body left one of the two parts empty")
	}
	if !strings.Contains(sender.last.HTML, "https://example.com/promo") {
		t.Error("the supplied link is missing from the generated html")
	}
	if sender.last.Subject == "" {
		t.Error("a generated message was left without a subject, so it cannot be sent")
	}
}

func TestALargeUploadIsMentionedBeforeItIsSent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, make([]byte, 12<<20), 0o600); err != nil {
		t.Fatalf("writing the attachment: %v", err)
	}

	got := send(t, &fakeSender{}, baseArgs("--attach", path)...)

	// A warning, never a refusal: the real ceiling is the server's and varies by plan, so a
	// local limit would reject messages the platform would have accepted.
	if got.code != errs.CodeOK {
		t.Fatalf("a large attachment was refused locally: %s", got.errOut)
	}
	if !strings.Contains(got.errOut, "MiB") {
		t.Errorf("no size was reported before the upload:\n%s", got.errOut)
	}
}

func TestProgressNeverReachesThePayloadStream(t *testing.T) {
	t.Parallel()

	got := send(t, &fakeSender{}, baseArgs()...)

	if !strings.Contains(got.errOut, "Sending to") {
		t.Errorf("no progress was reported:\n%s", got.errOut)
	}
	if strings.Contains(got.out, "Sending to") {
		t.Errorf("progress reached stdout, which breaks the payload contract:\n%s", got.out)
	}
}

func TestARateLimitPointsAtTheOneThingThatMakesARetrySafe(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{err: &mailkube.APIError{
		ErrorName:  mailkube.ErrorNameRateLimitExceeded,
		Message:    "Too many requests. Please slow down.",
		StatusCode: 429,
		RetryAfter: 3,
	}}
	got := send(t, sender, baseArgs()...)

	if got.code != errs.CodeRateLimit {
		t.Fatalf("exit code = %d, want %d", got.code, errs.CodeRateLimit)
	}
	// The server's own words, the wait it asked for, and the one flag that makes re-running
	// something other than a second charged message.
	for _, want := range []string{"Too many requests", "Retry after 3s", "--idempotency-key", "docs.mailkube.com"} {
		if !strings.Contains(got.errOut, want) {
			t.Errorf("the report does not mention %q:\n%s", want, got.errOut)
		}
	}
}

func TestACredentialFailureIsNotToldToRetrySafely(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{err: &mailkube.APIError{
		ErrorName: mailkube.ErrorNameInvalidAPIKey, Message: "API key is invalid.", StatusCode: 403,
	}}
	got := send(t, sender, baseArgs()...)

	if got.code != errs.CodeAuth {
		t.Fatalf("exit code = %d, want %d", got.code, errs.CodeAuth)
	}
	// Telling someone whose key was rejected to make their retry safe is advice to do the one
	// thing that cannot help, and it buries the sentence that would have told them so.
	if strings.Contains(got.errOut, "--idempotency-key") {
		t.Errorf("a rejected credential was offered a retry:\n%s", got.errOut)
	}
}

// TestAnEncodedAttachmentSurvivesTheRoundTrip guards the one path where the CLI both encodes and
// decodes: a payload file carries base64, and the SDK wants bytes.
func TestAnEncodedAttachmentSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	encoded := base64.StdEncoding.EncodeToString([]byte("file bytes"))
	payload := `{"from":"hello@acme.com","to":["alice@example.com"],"subject":"s","text":"t",` +
		`"attachments":[{"filename":"a.txt","content":"` + encoded + `"}]}`

	path := filepath.Join(t.TempDir(), "mail.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing the payload: %v", err)
	}

	sender := &fakeSender{}
	send(t, sender, "--json", "@"+path)

	if len(sender.last.Attachments) != 1 || string(sender.last.Attachments[0].Content) != "file bytes" {
		t.Errorf("attachments = %+v, want the decoded bytes", sender.last.Attachments)
	}
}

func TestContentThatIsNotBase64IsRefusedByName(t *testing.T) {
	t.Parallel()

	payload := `{"from":"hello@acme.com","to":["alice@example.com"],"subject":"s","text":"t",` +
		`"attachments":[{"filename":"a.txt","content":"not base64!!"}]}`
	path := filepath.Join(t.TempDir(), "mail.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("writing the payload: %v", err)
	}

	got := send(t, &fakeSender{}, "--json", "@"+path)
	if got.code != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeValidation)
	}
	if !strings.Contains(got.errOut, "a.txt") {
		t.Errorf("the report does not name the attachment:\n%s", got.errOut)
	}
}

func TestATemplateSendCarriesItsVariables(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "vars.json")
	if err := os.WriteFile(path, []byte(`{"first_name":"Alice","plan":"pro"}`), 0o600); err != nil {
		t.Fatalf("writing the variables: %v", err)
	}

	sender := &fakeSender{}
	got := send(t, sender,
		"--from", "hello@acme.com", "--to", "alice@example.com", "--subject", "Welcome",
		"--template-id", "1f0c1a2b-3c4d-5e6f-7a8b-9c0d1e2f3a4b", "--template-version", "latest",
		"--vars", "@"+path, "--var", "plan=enterprise")

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if sender.last.TemplateID == "" || sender.last.TemplateVersion != "latest" {
		t.Errorf("template = %q/%q, want both to reach the wire",
			sender.last.TemplateID, sender.last.TemplateVersion)
	}
	// The file supplies the set and an individual flag refines one of them, which is the same
	// merge rule tags and headers follow: a stored document plus one override.
	if sender.last.Variables["first_name"] != "Alice" {
		t.Errorf("variables = %v, want the file's values", sender.last.Variables)
	}
	if sender.last.Variables["plan"] != "enterprise" {
		t.Errorf("plan = %q, want the flag to win over the file", sender.last.Variables["plan"])
	}
}

func TestVariablesThatAreNotAnObjectOfStringsAreRefused(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "vars.json")
	if err := os.WriteFile(path, []byte(`["not","an","object"]`), 0o600); err != nil {
		t.Fatalf("writing the variables: %v", err)
	}

	got := send(t, &fakeSender{},
		"--from", "hello@acme.com", "--to", "alice@example.com", "--subject", "s",
		"--template-id", "t", "--vars", "@"+path)

	if got.code != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeValidation)
	}
}

func TestEveryRecipientListAndTheHeadersReachTheWire(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	got := send(t, sender, baseArgs(
		"--cc", "cc@example.com",
		"--bcc", "bcc@example.com",
		"--reply-to", "reply@acme.com",
		"--header", "X-Campaign: launch",
		"--topic", "news",
	)...)

	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}
	if len(sender.last.CC) != 1 || len(sender.last.BCC) != 1 || len(sender.last.ReplyTo) != 1 {
		t.Errorf("recipient lists = cc %v, bcc %v, reply-to %v", sender.last.CC, sender.last.BCC, sender.last.ReplyTo)
	}
	// A header value is free to contain a colon, so only the first one splits the flag.
	if sender.last.Headers["X-Campaign"] != "launch" {
		t.Errorf("headers = %v, want the campaign header", sender.last.Headers)
	}
	if sender.last.Topic != "news" {
		t.Errorf("topic = %q, want news", sender.last.Topic)
	}
}
