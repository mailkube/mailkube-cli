// Package input turns command-line values into the things commands actually work with: file
// contents, instants, and key/value pairs.
//
// Every rule here is shared by several flags across several features. Parsing a duration or a
// key=value pair in each command that takes one is how two flags end up accepting subtly different
// syntax, which a user then has to learn twice.
package input

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// FileRef is the prefix marking a value as a file to read rather than content to use.
const FileRef = "@"

// stdinRef is the whole value that means "read standard input".
const stdinRef = "@-"

// Reader resolves flag values that may name a file instead of carrying content.
//
// One rule covers every such flag: @path reads a file, @- reads standard input, and \@ escapes a
// literal leading @. Flags whose value is only ever a path take it bare, because there is nothing
// to disambiguate.
//
// It is a value rather than a function because standard input can only be read once, and that is
// a property of the invocation rather than of any one flag.
type Reader struct {
	stdin    io.Reader
	consumed bool
}

// NewReader returns a Reader that reads @- from stdin.
func NewReader(stdin io.Reader) *Reader {
	return &Reader{stdin: stdin}
}

// Resolve returns the content a flag value denotes.
//
// A value with no @ prefix is content already and comes back untouched, which is what keeps the
// common case free of ceremony.
func (r *Reader) Resolve(value string) (string, error) {
	switch {
	case value == stdinRef:
		return r.readStdin()
	case strings.HasPrefix(value, `\`+FileRef):
		// The escape exists so a literal value starting with @ is still expressible. Dropping
		// the backslash here is the whole of it.
		return value[1:], nil
	case strings.HasPrefix(value, FileRef):
		return readFile(strings.TrimPrefix(value, FileRef))
	default:
		return value, nil
	}
}

// readStdin reads standard input, once.
//
// A second @- in one invocation is a usage error rather than an empty string: standard input is
// consumed by the first read, so the second flag would silently receive nothing, and a command
// that quietly sends an empty body is worse than one that refuses to run.
func (r *Reader) readStdin() (string, error) {
	if r.consumed {
		return "", errs.Usagef("%s can be used once per command: standard input is a single stream", stdinRef)
	}
	r.consumed = true

	content, err := io.ReadAll(r.stdin)
	if err != nil {
		return "", errs.WithCode(errs.CodeValidation, err)
	}
	return string(content), nil
}

// readFile reads a named file, reporting the two failures a user can act on distinctly.
func readFile(path string) (string, error) {
	if path == "" {
		return "", errs.Usagef("%s needs a path, or %s to read standard input", FileRef, stdinRef)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return "", errs.Validationf("no such file: %s", path)
		case errors.Is(err, os.ErrPermission):
			return "", errs.Configf("cannot read %s: permission denied", path)
		default:
			return "", errs.WithCode(errs.CodeValidation, err)
		}
	}
	return string(content), nil
}
