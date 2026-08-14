package output

import (
	"bufio"
	"io"
	"strings"

	"golang.org/x/term"
)

// readNoEcho reads a line from a terminal with echo suppressed, reporting whether it could.
//
// It reads from the underlying stream rather than the buffered reader, which is safe only because
// nothing is buffered at this point: every prompt this package asks reads a whole line, so the
// buffer is drained between questions. Reporting failure rather than falling back internally
// keeps the decision with the caller, which is where the security judgement belongs.
func readNoEcho(raw io.Reader, buffered *bufio.Reader) (string, bool) {
	if buffered.Buffered() > 0 {
		return "", false
	}

	file, ok := raw.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", false
	}

	typed, err := term.ReadPassword(int(file.Fd()))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(typed)), true
}
