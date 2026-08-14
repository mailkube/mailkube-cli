package output

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// Confirmer asks the user to approve something irreversible.
//
// It is a value rather than a function because a single command may ask more than once and must
// keep reading from the same buffered input: a fresh bufio.Reader per question would discard
// whatever the first read buffered past the newline, and the second question would silently see
// nothing.
type Confirmer struct {
	in        *bufio.Reader
	errOut    io.Writer
	caps      Caps
	assumeYes bool
}

// NewConfirmer returns a Confirmer prompting on the given stream.
//
// assumeYes is the -y flag, resolved once for the whole invocation rather than passed to each
// question, because that is what it means: the user has approved this run, not one prompt in it.
//
// The prompt goes to the error stream, never to the success stream. stdout carries the payload
// and nothing else, so that a caller piping the command into a parser gets a document rather than
// a question.
func NewConfirmer(in io.Reader, errOut io.Writer, caps Caps, assumeYes bool) *Confirmer {
	return &Confirmer{in: bufio.NewReader(in), errOut: errOut, caps: caps, assumeYes: assumeYes}
}

// Confirm asks a yes/no question, defaulting to no.
//
// With -y the question is answered without being printed, which is what makes a destructive
// command scriptable.
//
// When the CLI cannot prompt — a pipe, a CI runner, a non-terminal of any kind — an unanswered
// question is a usage error rather than a default. Neither default is acceptable: assuming yes
// would cancel a batch nobody approved, and assuming no would make a scripted command fail in a
// way that reads like the operation was attempted and refused.
func (c *Confirmer) Confirm(question string) (bool, error) {
	if c.assumeYes {
		return true, nil
	}
	if !c.caps.Interactive {
		return false, errs.Usagef("%s\nThis needs an answer, and there is no terminal to ask on. Pass -y to confirm.",
			question)
	}

	if _, err := fmt.Fprintf(c.errOut, "%s (y/N) ", question); err != nil {
		return false, err
	}

	answer, err := c.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, errs.WithCode(errs.CodeUsage, err)
	}

	// A closed stream, a bare newline and anything that is not a yes all mean no. The default
	// is the safe one, because this question is only ever asked about something that cannot be
	// undone.
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
