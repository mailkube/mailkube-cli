package emails

import (
	"bytes"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// field is one line of the skeleton: a key, the empty value to fill in, and what it means.
type field struct {
	// Key is the wire name, which is also the key `--json` expects.
	Key string
	// Zero is the JSON literal a blank payload carries for this key.
	Zero string
	// Note is the annotation shown above the key in the human form.
	Note string
}

// skeletonFields is the annotated blank payload, in the order it renders.
//
// The keys are the Payload's, and a test pins that correspondence against a request the SDK
// actually sent, so a field cannot exist on the wire and be missing from what a user is handed to
// fill in. The annotations are written rather than derived: a constraint is prose, and generating
// "max 255 characters" out of a struct tag would mean inventing a tag vocabulary to hold it.
//
// The constraints named here are the platform's, not a plan's. Where a plan can lower a ceiling,
// the note says so instead of asserting a number this program cannot know.
func skeletonFields() []field {
	return []field{
		{"from", `""`, `required. Display-name form allowed: "Acme <hello@acme.com>"`},
		{"to", `[""]`, "required. Your plan's recipient limit applies."},
		{"cc", `[]`, ""},
		{"bcc", `[]`, ""},
		{"reply_to", `[]`, ""},
		{"subject", `""`, "required. Max 255 characters, no line breaks."},
		{"html", `""`, "one of html / text / template_id"},
		{"text", `""`, ""},
		{"template_id", `""`, "mutually exclusive with html and text"},
		{"template_version", `""`, "defaults to the published version"},
		{"variables", `{}`, "values for the template's placeholders"},
		{"topic", `""`, "max 16 characters"},
		{"tags", `[]`, "max 20; name<=16, value<=32, [A-Za-z0-9_-], unique names"},
		{"headers", `{}`, "max 20; name<=64, value<=998, no line breaks"},
		{"attachments", `[]`, "{filename, content(base64), content_type}"},
		{"scheduled_at", `""`, "RFC 3339 with an offset, in the future"},
		{"batch_id", `""`, "only valid alongside scheduled_at"},
	}
}

// SkeletonView is a blank payload to edit and feed back in through --json.
//
// It exists because `emails send` has seventeen fields and a command line is a poor editor for a
// document. The human form is annotated so the constraints are readable where they are needed;
// every machine form is plain JSON, so it can be piped into something that fills it in.
//
// Notably absent: idempotency_key. It travels as a header rather than in the body, so a key
// written into this file would be silently ignored; --idempotency-key supplies it.
type SkeletonView struct{}

// RenderText implements output.TextRenderer, emitting the annotated form.
func (SkeletonView) RenderText(_ output.Caps) []string {
	fields := skeletonFields()
	lines := []string{"{"}

	for i, f := range fields {
		if f.Note != "" {
			lines = append(lines, "  // "+f.Note)
		}
		line := "  " + quote(f.Key) + ": " + f.Zero
		if i < len(fields)-1 {
			line += ","
		}
		lines = append(lines, line)
	}
	return append(lines, "}")
}

// MarshalJSON implements json.Marshaler, emitting the same keys without the annotations.
//
// Written by hand rather than through a map so the key order matches the human form. A generator
// consuming this and a person reading the annotated version should be looking at one document.
func (SkeletonView) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{")

	for i, f := range skeletonFields() {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(quote(f.Key))
		b.WriteString(":")
		b.WriteString(f.Zero)
	}

	b.WriteString("}")
	return b.Bytes(), nil
}

// quote renders a JSON string key. The keys are this file's own constants, all of them plain
// lowercase identifiers, so there is nothing here to escape.
func quote(s string) string { return `"` + s + `"` }

// SkeletonKeys returns the keys of a blank payload, for the test that pins them to the wire.
func SkeletonKeys() []string {
	fields := skeletonFields()
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		keys = append(keys, f.Key)
	}
	return keys
}

// skeletonNotes is used by the help text to point at the flag that emits this.
func skeletonNotes() string {
	return strings.Join([]string{
		"emit an annotated blank payload and exit",
		"(pipe it to a file, edit it, then send it with --json @file)",
	}, " ")
}
