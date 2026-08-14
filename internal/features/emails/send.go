package emails

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/clientfactory"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// The flag names, referenced when asking whether the user set one.
//
// A flag's default and the same value typed out explicitly have to be told apart, because that is
// what makes `--json` a base that flags refine rather than a mode that flags are ignored in.
const (
	flagFrom            = "from"
	flagTo              = "to"
	flagCC              = "cc"
	flagBCC             = "bcc"
	flagReplyTo         = "reply-to"
	flagSubject         = "subject"
	flagHTML            = "html"
	flagText            = "text"
	flagTemplateID      = "template-id"
	flagTemplateVersion = "template-version"
	flagTopic           = "topic"
	flagAt              = "at"
	flagBatchID         = "batch-id"
)

// sizeWarnThreshold is where the CLI mentions the size of what is about to be uploaded.
//
// It is a warning and never a refusal. The real ceiling is the server's and varies by plan, so a
// local limit would reject messages the platform would have accepted; what the CLI can usefully
// do is say how much is about to go up, before a slow link spends ten minutes on it.
const sizeWarnThreshold = 10 << 20

// sendOptions is everything `emails send` accepts.
type sendOptions struct {
	from                      string
	to, cc, bcc, replyTo      []string
	subject                   string
	html, text                string
	templateID                string
	templateVersion           string
	vars                      *input.PairFlag
	varsFile                  string
	tags                      *input.PairFlag
	headers                   *input.PairFlag
	topic                     string
	attach                    []string
	attachTypes               *input.PairFlag
	at                        string
	batchID                   string
	idempotencyKey            string
	payloadRef                string
	skeleton                  bool
	sample                    bool
	sampleLinks, sampleImages []string
	dryRun                    bool
}

// sendCmd builds `emails send`.
func (f *Feature) sendCmd(deps *feature.Deps) *cobra.Command {
	o := &sendOptions{
		vars:        input.NewVarFlag(),
		tags:        input.NewTagFlag(),
		headers:     input.NewHeaderFlag(),
		attachTypes: &input.PairFlag{Separator: "=", Noun: "attachment type", Example: "--attach-type report.pdf=application/pdf"},
	}

	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send an email",
		Long: "Send an email over the API.\n\n" +
			"Every send from this tool is a real, charged message that affects your sending\n" +
			"reputation. There is no sandbox and no test key. Use --dry-run first.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return f.runSend(c, deps, o)
		},
	}
	o.register(cmd)
	return cmd
}

// register declares the flags. The order is the order they read in help.
func (o *sendOptions) register(cmd *cobra.Command) {
	fs := cmd.Flags()

	fs.StringVar(&o.from, flagFrom, "", "sender address, optionally with a display name")
	fs.StringSliceVar(&o.to, flagTo, nil, "recipient address (repeatable)")
	fs.StringSliceVar(&o.cc, flagCC, nil, "carbon-copy recipient (repeatable)")
	fs.StringSliceVar(&o.bcc, flagBCC, nil, "blind carbon-copy recipient (repeatable)")
	fs.StringSliceVar(&o.replyTo, flagReplyTo, nil, "Reply-To address (repeatable)")
	fs.StringVar(&o.subject, flagSubject, "", "subject line")

	fs.StringVar(&o.html, flagHTML, "", "HTML body, or @file, or @- for stdin")
	fs.StringVar(&o.text, flagText, "", "plain-text body, or @file, or @- for stdin")
	fs.StringVar(&o.templateID, flagTemplateID, "", "saved template to render instead of a body")
	fs.StringVar(&o.templateVersion, flagTemplateVersion, "", "template version, or latest")
	fs.Var(o.vars, "var", "template variable, as key=value (repeatable)")
	fs.StringVar(&o.varsFile, "vars", "", "template variables as a JSON object: @file or @-")

	fs.Var(o.tags, "tag", "message tag, as name=value (repeatable)")
	fs.Var(o.headers, "header", "custom header, as 'Name: value' (repeatable)")
	fs.StringVar(&o.topic, flagTopic, "", "mailing-list topic slug")

	fs.StringArrayVar(&o.attach, "attach", nil, "path to a file to attach (repeatable)")
	fs.Var(o.attachTypes, "attach-type", "override an attachment's type, as filename=mime (repeatable)")

	fs.StringVar(&o.at, flagAt, "", "schedule the send: RFC 3339 with an offset, or +2h")
	fs.StringVar(&o.batchID, flagBatchID, "", "group this scheduled send under a batch label")
	fs.StringVar(&o.idempotencyKey, "idempotency-key", "",
		"make a retry safe; "+AutoKey+" derives one from the message")

	fs.StringVar(&o.payloadRef, "json", "", "full payload as JSON: @file or @-")
	fs.BoolVar(&o.skeleton, "generate-skeleton", false, skeletonNotes())
	fs.BoolVar(&o.sample, "sample", false, "generate a body with links and images")
	fs.StringArrayVar(&o.sampleLinks, "link", nil, "link to include in the generated body (repeatable)")
	fs.StringArrayVar(&o.sampleImages, "image", nil, "image to include in the generated body (repeatable)")

	fs.BoolVar(&o.dryRun, "dry-run", false, "print the request that would be sent, and send nothing")
}

// runSend is the whole command: build, check, then either show or send.
func (f *Feature) runSend(cmd *cobra.Command, deps *feature.Deps, o *sendOptions) error {
	if o.skeleton {
		return deps.Emit(SkeletonView{})
	}

	payload, err := o.payload(cmd, deps)
	if err != nil {
		return err
	}
	if err := Validate(payload); err != nil {
		return err
	}

	resolved, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return err
	}

	if o.dryRun {
		return emitDryRun(deps, resolved, payload)
	}
	return f.submit(cmd.Context(), deps, resolved, o, payload)
}

// payload assembles the message from a JSON base and the flags that refine it.
func (o *sendOptions) payload(cmd *cobra.Command, deps *feature.Deps) (Payload, error) {
	// One reader per invocation, because standard input can be read once and which flag got
	// there first is a property of the command line rather than of any single flag.
	reader := input.NewReader(deps.IO.In)

	payload, err := o.base(reader)
	if err != nil {
		return Payload{}, err
	}
	if err := o.applyFlags(cmd, reader, &payload); err != nil {
		return Payload{}, err
	}
	if err := o.applySample(deps, &payload); err != nil {
		return Payload{}, err
	}
	if err := o.applyAttachments(&payload); err != nil {
		return Payload{}, err
	}
	if err := o.applySchedule(cmd, deps, &payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

// base reads the --json payload, or returns an empty one.
//
// Unknown keys are rejected rather than ignored. A payload file is edited by hand, and a
// misspelled key silently dropped is a message sent without the body its author wrote.
func (o *sendOptions) base(reader *input.Reader) (Payload, error) {
	if o.payloadRef == "" {
		return Payload{}, nil
	}

	content, err := reader.Resolve(o.payloadRef)
	if err != nil {
		return Payload{}, err
	}

	var payload Payload
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, errs.Validationf(
			"--json is not a valid send payload: %v\nRun `mailkube emails send --generate-skeleton` for the shape.",
			err)
	}
	return payload, nil
}

// applyFlags lets an explicitly given flag win over the JSON base.
func (o *sendOptions) applyFlags(cmd *cobra.Command, reader *input.Reader, p *Payload) error {
	changed := cmd.Flags().Changed

	o.applyRouting(changed, p)
	if err := o.applyContent(changed, reader, p); err != nil {
		return err
	}
	return o.applyLabels(changed, reader, p)
}

// applyRouting sets the envelope: who it is from, and who it goes to.
func (o *sendOptions) applyRouting(changed func(string) bool, p *Payload) {
	if changed(flagFrom) {
		p.From = o.from
	}
	if changed(flagTo) {
		p.To = o.to
	}
	if changed(flagCC) {
		p.CC = o.cc
	}
	if changed(flagBCC) {
		p.BCC = o.bcc
	}
	if changed(flagReplyTo) {
		p.ReplyTo = o.replyTo
	}
	if changed(flagSubject) {
		p.Subject = o.subject
	}
}

// applyContent sets the body, or the template that stands in for one.
func (o *sendOptions) applyContent(changed func(string) bool, reader *input.Reader, p *Payload) error {
	if changed(flagHTML) {
		html, err := reader.Resolve(o.html)
		if err != nil {
			return err
		}
		p.HTML = html
	}
	if changed(flagText) {
		text, err := reader.Resolve(o.text)
		if err != nil {
			return err
		}
		p.Text = text
	}
	if changed(flagTemplateID) {
		p.TemplateID = o.templateID
	}
	if changed(flagTemplateVersion) {
		p.TemplateVersion = o.templateVersion
	}
	return o.applyVariables(reader, p)
}

// applyVariables merges the template variables, file first and then individual flags.
func (o *sendOptions) applyVariables(reader *input.Reader, p *Payload) error {
	if o.varsFile != "" {
		content, err := reader.Resolve(o.varsFile)
		if err != nil {
			return err
		}
		var vars map[string]string
		if err := json.Unmarshal([]byte(content), &vars); err != nil {
			return errs.Validationf("--vars is not a JSON object of string values: %v", err)
		}
		p.Variables = merge(p.Variables, vars)
	}

	for _, pair := range o.vars.Pairs() {
		p.Variables = merge(p.Variables, map[string]string{pair.Key: pair.Value})
	}
	return nil
}

// applyLabels merges the tags, headers and topic.
//
// Tags and headers merge by key rather than replacing the list wholesale, which is what makes a
// stored payload plus one flag a useful combination: --tag run=17 on a file that already carries
// a campaign tag adds to it, and repeating a name overrides that one entry.
func (o *sendOptions) applyLabels(changed func(string) bool, _ *input.Reader, p *Payload) error {
	if changed(flagTopic) {
		p.Topic = o.topic
	}

	for _, pair := range o.tags.Pairs() {
		p.Tags = mergeTag(p.Tags, Tag{Name: pair.Key, Value: pair.Value})
	}
	for _, pair := range o.headers.Pairs() {
		p.Headers = merge(p.Headers, map[string]string{pair.Key: pair.Value})
	}
	return nil
}

// applySample fills in a generated body when one was asked for.
//
// It refuses rather than overwriting: --sample beside --html is two answers to one question, and
// picking one silently means a user watches their own body disappear.
func (o *sendOptions) applySample(deps *feature.Deps, p *Payload) error {
	if !o.sample {
		if len(o.sampleLinks) > 0 || len(o.sampleImages) > 0 {
			return errs.Usagef("--link and --image describe the generated body, so they need --sample")
		}
		return nil
	}
	if p.HTML != "" || p.Text != "" || p.TemplateID != "" {
		return errs.Usagef("--sample generates the body, so it cannot be combined with --html, --text or --template-id")
	}

	// Seeded from the injected clock, so a run is reproducible under a fixed one and varies
	// under the real one: two sample sends should not be byte-identical messages.
	sample := GenerateSample(uint64(deps.Clock.Now().UnixNano()), o.sampleLinks, o.sampleImages)
	p.HTML, p.Text = sample.HTML, sample.Text
	if p.Subject == "" {
		p.Subject = SampleSubject
	}
	return nil
}

// applyAttachments reads each file and encodes it the way the wire carries it.
func (o *sendOptions) applyAttachments(p *Payload) error {
	types := map[string]string{}
	for _, pair := range o.attachTypes.Pairs() {
		types[pair.Key] = pair.Value
	}

	for _, path := range o.attach {
		content, err := os.ReadFile(path) //nolint:gosec // the path is the user's own argument
		if err != nil {
			return errs.Validationf("cannot attach %s: %v", path, err)
		}
		name := filepath.Base(path)
		p.Attachments = append(p.Attachments, Attachment{
			Filename:    name,
			Content:     base64.StdEncoding.EncodeToString(content),
			ContentType: types[name],
		})
	}

	for name := range types {
		if !attached(p.Attachments, name) {
			return errs.Usagef("--attach-type names %q, which is not attached", name)
		}
	}
	return nil
}

// attached reports whether a filename is among the attachments.
func attached(attachments []Attachment, name string) bool {
	for _, a := range attachments {
		if a.Filename == name {
			return true
		}
	}
	return false
}

// applySchedule resolves --at and --batch-id.
func (o *sendOptions) applySchedule(cmd *cobra.Command, deps *feature.Deps, p *Payload) error {
	if cmd.Flags().Changed(flagAt) {
		at, err := input.ParseAt(o.at, deps.Clock.Now())
		if err != nil {
			return err
		}
		p.ScheduledAt = at.Format(time.RFC3339)
	}
	if cmd.Flags().Changed(flagBatchID) {
		p.BatchID = o.batchID
	}
	return nil
}

// merge returns a map with the additions applied, without mutating a caller's map.
func merge(base, additions map[string]string) map[string]string {
	if len(additions) == 0 {
		return base
	}

	merged := make(map[string]string, len(base)+len(additions))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range additions {
		merged[k] = v
	}
	return merged
}

// mergeTag adds a tag, replacing any existing one with the same name.
func mergeTag(tags []Tag, tag Tag) []Tag {
	for i, existing := range tags {
		if existing.Name == tag.Name {
			tags[i] = tag
			return tags
		}
	}
	return append(tags, tag)
}

// emitDryRun prints the request that would have been made.
func emitDryRun(deps *feature.Deps, r settings.Resolved, p Payload) error {
	body, err := indentedJSON(p.Elided())
	if err != nil {
		return errs.WithCode(errs.CodeInternal, err)
	}

	return deps.Emit(DryRunView{
		Method: "POST",
		URL:    clientfactory.NormalizeBaseURL(r.BaseURL.Value) + "emails",
		Body:   body,
		DryRun: true,
	})
}

// indentedJSON renders a value the way the rest of this CLI renders JSON.
//
// The standard library escapes <, > and & into \u sequences by default, for safety in an HTML
// page. Nothing here is embedded in one, and a preview that renders a display-name sender as
// "Acme <hello@acme.com>" is unreadable next to the address the user actually typed.
func indentedJSON(v any) ([]byte, error) {
	var buf bytes.Buffer

	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// submit sends the message and reports what came back.
func (f *Feature) submit(
	ctx context.Context, deps *feature.Deps, r settings.Resolved, o *sendOptions, p Payload,
) error {
	if err := clientfactory.RequireAPIKey(clientfactory.Settings{APIKey: r.APIKey.Value}); err != nil {
		return err
	}

	key, err := o.resolveKey(p)
	if err != nil {
		return err
	}
	params, err := p.Params(key)
	if err != nil {
		return err
	}

	sender, err := f.sender(deps, r)
	if err != nil {
		return err
	}

	if size := p.EncodedSize(); size >= sizeWarnThreshold {
		deps.Progress("Uploading %s of attachments; your plan's message size limit applies.", humanSize(size))
	}
	deps.Progress("Sending to %s…", strings.Join(p.To, ", "))

	email, err := sender.Send(ctx, params)
	if err != nil {
		return advise(err)
	}
	return deps.Emit(sentView(deps, email, p))
}

// resolveKey turns --idempotency-key into the header value the SDK sends.
func (o *sendOptions) resolveKey(p Payload) (string, error) {
	if o.idempotencyKey != AutoKey {
		return o.idempotencyKey, nil
	}
	return DeriveKey(p)
}

// sentView describes what the server accepted.
//
// The recipients come from the payload rather than the response, because a send acknowledgement
// carries an id and not a copy of the request. Reporting what was accepted for whom is the point
// of the line, and taking it from what was sent is the only place it exists.
func sentView(deps *feature.Deps, email *mailkube.Email, p Payload) SentView {
	view := SentView{
		ID:        email.ID,
		MessageID: email.MessageID,
		To:        p.To,
		Replayed:  email.IdempotentReplayed,
	}
	if email.IsScheduled() {
		view.Scheduled = true
		view.Status = email.Status
		view.ScheduledAt = email.ScheduledAt
		view.BatchID = email.BatchID
		view.due = dueText(deps.Clock.Now(), email.ScheduledAt)
	}
	return view
}

// dueText renders a scheduled time as an instant and as a distance from now.
func dueText(now time.Time, value string) string {
	human := humanTime(value)

	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return human
	}
	return human + "   (" + relative(now, at) + ")"
}

// advise adds what this command in particular has to say about a failure.
//
// The kernel's default retry sentence is deliberately generic, because whether re-running is safe
// depends on what the command does. Here it is not: a second send is a second charged message
// unless it carries an idempotency key, and that is the single most useful thing to say to
// someone staring at a 429.
//
// It is said only where re-running is the question. Telling someone whose key was rejected, or
// whose from domain was refused, to make their retry safe is advice to do the one thing that
// cannot help — the request will be refused identically, and the note buries the sentence that
// would have told them so.
func advise(err error) error {
	const note = "This command does not retry. Re-run it, or use --idempotency-key to make a retry safe."

	switch errs.CodeFor(err) {
	case errs.CodeRateLimit:
		return errs.Advise(err, note, "Your plan's send rate: https://docs.mailkube.com/docs/limits")
	case errs.CodeServer, errs.CodeNetwork, errs.CodeDeadline:
		return errs.Advise(err, note)
	default:
		return err
	}
}
