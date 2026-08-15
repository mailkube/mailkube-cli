package output

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// Prompter asks the user questions: a yes/no confirmation, a line of text, or a secret.
//
// It is a value rather than a set of functions because a single command may ask more than once
// and must keep reading from the same buffered input: a fresh bufio.Reader per question would
// discard whatever the first read buffered past the newline, and the second question would
// silently see nothing.
type Prompter struct {
	raw       io.Reader
	in        *bufio.Reader
	errOut    io.Writer
	caps      Caps
	assumeYes bool
}

// NewPrompter returns a Prompter asking on the given stream.
//
// assumeYes is the -y flag, resolved once for the whole invocation rather than passed to each
// question, because that is what it means: the user has approved this run, not one prompt in it.
//
// The prompt goes to the error stream, never to the success stream. stdout carries the payload
// and nothing else, so that a caller piping the command into a parser gets a document rather than
// a question.
func NewPrompter(in io.Reader, errOut io.Writer, caps Caps, assumeYes bool) *Prompter {
	return &Prompter{raw: in, in: bufio.NewReader(in), errOut: errOut, caps: caps, assumeYes: assumeYes}
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
func (c *Prompter) Confirm(question string) (bool, error) {
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

// Line asks for a value and returns what was typed, with surrounding space removed.
//
// It never falls back to a default. A command that reaches here needs a value it was not given,
// and inventing one would store a credential or a hostname the user never chose.
//
// The error for the unanswerable case is supplied by the caller rather than built from the
// question, because a question and a diagnosis read differently: "Paste your API key:" is the
// right thing to show a person and the wrong thing to hand a script, which needs to be told
// which flag or variable supplies the value instead.
func (c *Prompter) Line(question string, unanswerable error) (string, error) {
	if !c.caps.Interactive {
		return "", unanswerable
	}
	if _, err := fmt.Fprintf(c.errOut, "%s ", question); err != nil {
		return "", err
	}

	answer, err := c.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errs.WithCode(errs.CodeUsage, err)
	}
	return strings.TrimSpace(answer), nil
}

// Secret asks for a value without echoing it.
//
// Echo is suppressed through the terminal itself when the input really is one. When it is not —
// a test, a pipe — there is nothing to suppress, and the read falls back to an ordinary line.
// That fallback is safe precisely because Line and Confirm already refuse to prompt at all
// unless both streams are terminals, so the non-terminal case never reaches a real user.
func (c *Prompter) Secret(question string, unanswerable error) (string, error) {
	if !c.caps.Interactive {
		return "", unanswerable
	}
	if _, err := fmt.Fprintf(c.errOut, "%s ", question); err != nil {
		return "", err
	}

	if secret, ok := readNoEcho(c.raw, c.in); ok {
		// The newline the terminal did not echo, so the next line does not run into the prompt.
		_, _ = fmt.Fprintln(c.errOut)
		return secret, nil
	}

	answer, err := c.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errs.WithCode(errs.CodeUsage, err)
	}
	return strings.TrimSpace(answer), nil
}
