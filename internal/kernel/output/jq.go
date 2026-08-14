package output

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// Project writes a value through a jq expression.
//
// The query engine is embedded rather than shelled out to. A CLI whose documented scripting
// examples pipe into jq works on the maintainer's machine and fails on Windows, where jq is not
// installed and there is no package manager convention that would put it there. Embedding makes
// the examples in the documentation run everywhere, which is the only reason to have them.
//
// A result that is a string is written raw, without quotes. This is what makes the common case
// work: `--jq .id` exists to be captured into a shell variable, and a value wrapped in quotes has
// to be stripped by the caller before it can be used.
func Project(w io.Writer, expr string, v any) error {
	query, err := gojq.Parse(expr)
	if err != nil {
		return errs.Usagef("cannot parse the --jq expression: %v", err)
	}

	// gojq operates on the shapes encoding/json produces, so the value goes through JSON first.
	// That also means the expression is written against the documented field names rather than
	// against Go field names, so an example from the API documentation works unaltered.
	generic, err := toGeneric(v)
	if err != nil {
		return err
	}

	iter := query.Run(generic)
	for {
		result, ok := iter.Next()
		if !ok {
			return nil
		}
		if err := writeResult(w, result); err != nil {
			return err
		}
	}
}

// writeResult writes one query result, or reports the query's own failure.
func writeResult(w io.Writer, result any) error {
	if err, ok := result.(error); ok {
		// A jq runtime error is the user's expression meeting the actual data — indexing a
		// string, say. That is a usage problem, and reporting it as a server error would send
		// them looking in the wrong place entirely.
		return errs.Usagef("the --jq expression failed: %v", err)
	}

	if s, ok := result.(string); ok {
		_, err := fmt.Fprintln(w, s)
		return err
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}
