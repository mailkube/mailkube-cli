package cli

import (
	"fmt"
	"io"
	"runtime/debug"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// issueTracker is where a user reports a defect in this program.
const issueTracker = "https://github.com/mailkube/mailkube-cli/issues"

// Run executes the command tree and returns the process exit code.
//
// Everything above this returns an error; this is the single point where one becomes an exit
// code. It lives here rather than in main so that the whole path — including how a failure and a
// crash are rendered — is exercised by the same tests that check every other screen. main's only
// remaining job is to build the real dependencies and end the process.
//
// The report goes to the error stream, never to the payload stream: on failure stdout stays
// empty, so a caller piping the output into a parser never sees half a document.
func Run(deps *feature.Deps, args []string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = reportPanic(deps.IO.ErrOut, r, debug.Stack())
		}
	}()

	root := NewRootCmd(deps)
	// A nil slice is not the same as no arguments to cobra: it falls back to the process's own
	// os.Args, which is how a caller that meant "run with nothing" gets the test binary's flags.
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		return report(deps, err)
	}
	return int(errs.CodeOK)
}

// report renders a failure and returns its exit code.
func report(deps *feature.Deps, err error) int {
	detail := errs.Describe(err)
	for _, line := range errs.Render(detail, deps.Caps.Glyphs.Cross) {
		// If reporting fails there is nothing left to report it with, and the exit code still
		// carries the outcome. Discarding the error explicitly says that is deliberate.
		_, _ = fmt.Fprintln(deps.IO.ErrOut, line)
	}
	return int(detail.Code)
}

// reportPanic turns a crash into an exit code, a short report and a stack trace.
//
// A panic is the one outcome that is this program's fault rather than the user's or the server's,
// so it gets its own code: every other code tells the caller something about their request, and
// conflating a defect with any of them would send them looking for a problem they do not have.
//
// The trace is printed because the alternative is a bug report that says "it crashed".
func reportPanic(errOut io.Writer, recovered any, stack []byte) int {
	_, _ = fmt.Fprintf(errOut, "✗ internal error: %v\n", recovered)
	_, _ = fmt.Fprintf(errOut, "  This is a defect in mailkube, not a problem with your request.\n")
	_, _ = fmt.Fprintf(errOut, "  Please report it, with the trace below: %s\n\n", issueTracker)
	_, _ = errOut.Write(stack)
	return int(errs.CodeInternal)
}
