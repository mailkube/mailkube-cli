// Command mailkube is the Mailkube command-line interface.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// issueTracker is where a user reports a defect in this program.
const issueTracker = "https://github.com/mailkube/mailkube-cli/issues"

// main is the only place in the program that ends the process.
func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run executes the command tree and returns the process exit code.
//
// Everything above this returns an error; this is the single point where one becomes an exit
// code. Keeping it separate from main is what makes the whole path testable — a test calls run
// and inspects the returned code, instead of watching the test binary exit.
//
// The report goes to the error stream, never to the payload stream: on failure stdout stays
// empty, so a caller piping the output into a parser never sees half a document.
func run(args []string, in io.Reader, out, errOut io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			code = reportPanic(errOut, r, debug.Stack())
		}
	}()

	streams := &feature.IOStreams{In: in, Out: out, ErrOut: errOut}

	root := cli.NewRootCmd(&feature.Deps{IO: streams})
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		return report(errOut, err)
	}
	return int(errs.CodeOK)
}

// report renders a failure and returns its exit code.
func report(errOut io.Writer, err error) int {
	detail := errs.Describe(err)
	for _, line := range errs.Render(detail, "✗") {
		// If reporting fails there is nothing left to report it with, and the exit code still
		// carries the outcome. Discarding the error explicitly says that is deliberate.
		_, _ = fmt.Fprintln(errOut, line)
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
