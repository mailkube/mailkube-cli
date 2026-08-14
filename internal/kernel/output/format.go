package output

import (
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// Format is how a command's result is written to the success stream.
type Format int

const (
	// Text is the human rendering: badges, aligned columns, shortened ids.
	Text Format = iota
	// JSON is one indented object or array.
	JSON
	// NDJSON is one compact object per line, for streams and for line-oriented tools.
	NDJSON
	// YAML is the same data as JSON, in a form that is easier to read at a glance.
	YAML
)

// String returns the flag spelling of the format.
func (f Format) String() string {
	switch f {
	case Text:
		return "text"
	case JSON:
		return "json"
	case NDJSON:
		return "ndjson"
	case YAML:
		return "yaml"
	default:
		return "unknown"
	}
}

// ParseFormat resolves the --output flag's value.
func ParseFormat(value string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "text":
		return Text, nil
	case "json":
		return JSON, nil
	case "ndjson":
		return NDJSON, nil
	case "yaml", "yml":
		return YAML, nil
	default:
		return Text, errs.Usagef(
			"unknown output format %q: use text, json, ndjson or yaml", value)
	}
}

// Resolve decides the output format, preferring what the user asked for.
//
// With no --output flag the format follows the destination: human text on a terminal, JSON
// anywhere else. That is the rule that makes the CLI scriptable without a flag — a pipe, a
// subshell, a CI runner and a `$(…)` capture all get parseable output by default, and none of
// them has to remember to ask.
//
// Inferring in the other direction would be worse than having no rule: a script that forgot the
// flag would get a table, and the failure would be a parse error somewhere downstream rather than
// at the point of the mistake.
func Resolve(explicit string, caps Caps) (Format, error) {
	if strings.TrimSpace(explicit) != "" {
		return ParseFormat(explicit)
	}
	if caps.TTY {
		return Text, nil
	}
	return JSON, nil
}
