package output

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"gopkg.in/yaml.v3"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// TextRenderer is implemented by a view model that knows its own human rendering.
//
// It returns lines rather than writing them, so the same value can be rendered into a golden
// file, into a buffer, or into a terminal without knowing which. Caps arrives as an argument
// because the rendering depends on the badge set and the available width, and a view model that
// reached for those globally would be untestable at exactly the point where the tests matter.
type TextRenderer interface {
	// RenderText returns the lines of the human form, without trailing newlines.
	RenderText(caps Caps) []string
}

// Render writes a view model to the success stream in the chosen format.
//
// Every format renders the same value. That is the whole point of the view model: the human form
// and the machine form cannot drift apart, because neither is produced from the other and both
// are produced from one struct.
func Render(w io.Writer, format Format, caps Caps, v any) error {
	switch format {
	case Text:
		return renderText(w, caps, v)
	case JSON:
		return renderJSON(w, v)
	case NDJSON:
		return renderNDJSON(w, v)
	case YAML:
		return renderYAML(w, v)
	default:
		return errs.Usagef("unknown output format %q", format)
	}
}

// renderText writes the human form, one line at a time.
func renderText(w io.Writer, caps Caps, v any) error {
	r, ok := v.(TextRenderer)
	if !ok {
		// This is a programming error rather than a user error: a command returned something
		// with no human rendering, and there is no sensible fallback. Printing the Go value
		// would put %+v output in front of a user, which is how a debug print ships.
		return errs.Newf(errs.CodeInternal, "%T has no text rendering", v)
	}
	return WriteLines(w, r.RenderText(caps))
}

// WriteLines writes lines to a stream, each terminated with a newline.
func WriteLines(w io.Writer, lines []string) error {
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

// renderJSON writes one indented document.
func renderJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// HTML escaping turns an ampersand in a subject line into &, which is valid JSON and
	// unreadable to a human comparing it against what they sent. Nothing here is embedded in a
	// web page.
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// renderNDJSON writes one compact document per line, expanding a slice into its elements.
//
// Expanding is what makes the format worth having: a listing rendered as a single JSON array on
// one line is not line-oriented, and the tools this format exists for read a record per line.
func renderNDJSON(w io.Writer, v any) error {
	for _, item := range elements(v) {
		if err := encodeLine(w, item); err != nil {
			return err
		}
	}
	return nil
}

// elements returns the items of a slice or array, or the value itself for anything else.
func elements(v any) []any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return []any{v}
	}

	items := make([]any, rv.Len())
	for i := range items {
		items[i] = rv.Index(i).Interface()
	}
	return items
}

// encodeLine writes one compact JSON document followed by a newline.
func encodeLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v) // Encode already terminates with a newline
}

// renderYAML writes the JSON shape as YAML.
//
// It goes through JSON deliberately. The view models carry json tags, which are the documented,
// semver-stable field names, and encoding them directly as YAML would use Go field names instead
// — so `-o yaml` and `-o json` would disagree about what a field is called, and only one of them
// would match the contract.
func renderYAML(w io.Writer, v any) error {
	node, err := jsonToNode(v)
	if err != nil {
		return err
	}

	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return err
	}
	return enc.Close()
}

// toGeneric round-trips a value through JSON into maps and slices.
//
// This is what makes the json tags the single source of truth for every machine format.
func toGeneric(v any) (any, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return nil, err
	}
	return generic, nil
}
