package feature

import (
	"bytes"
	"io"
)

// IOStreams are the three streams a command may read from and write to.
//
// Nothing in the CLI touches the process's own streams directly (a lint rule enforces it), which
// is what makes every screen assertable: a test constructs Test streams, runs a command, and
// compares the captured bytes against a golden file.
//
// The split is a contract, not a convention: Out carries the success payload and nothing else,
// so a caller can pipe it into a parser, and every progress line, warning, prompt and error goes
// to ErrOut. On failure Out stays empty.
type IOStreams struct {
	// In is the input stream, for reading stdin with the @- convention.
	In io.Reader
	// Out is the success payload. Nothing else is ever written here.
	Out io.Writer
	// ErrOut carries progress, warnings, prompts, hints and errors.
	ErrOut io.Writer
}

// TestStreams returns streams backed by buffers, plus the two buffers to assert on.
//
// The buffers are returned rather than reachable through the struct so a test cannot
// accidentally assert on the wrong one:
//
//	io, out, errOut := feature.TestStreams()
func TestStreams() (streams *IOStreams, out, errOut *bytes.Buffer) {
	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	return &IOStreams{In: &bytes.Buffer{}, Out: out, ErrOut: errOut}, out, errOut
}
