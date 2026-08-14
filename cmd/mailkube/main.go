// Command mailkube is the Mailkube command-line interface.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

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
// The error goes to the error stream, never to the payload stream: on failure stdout stays
// empty, so a caller piping the output into a parser never sees half a document.
func run(args []string, in io.Reader, out, errOut io.Writer) int {
	streams := &feature.IOStreams{In: in, Out: out, ErrOut: errOut}

	root := cli.NewRootCmd(&feature.Deps{IO: streams})
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		// If reporting the error fails there is nothing left to report it with, and the exit
		// code still carries the outcome. Discarding it explicitly says that is deliberate.
		_, _ = fmt.Fprintf(errOut, "✗ %v\n", err)
		return 1
	}
	return 0
}
