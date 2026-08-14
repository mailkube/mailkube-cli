// Command mailkube is the Mailkube command-line interface.
package main

import (
	"io"
	"os"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// main is the only place in the program that ends the process.
func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run builds the real dependencies and hands them to the command tree.
//
// Everything below this point is injected, which is what makes every screen assertable: the
// streams, the clock and the terminal model all arrive from here, and a test supplies its own.
// The terminal is inspected once, here, and passed down — a command that re-detected colour
// support per line could disagree with itself mid-screen.
func run(args []string, in io.Reader, out, errOut io.Writer) int {
	env := output.OSEnv()

	return cli.Run(&feature.Deps{
		IO:    &feature.IOStreams{In: in, Out: out, ErrOut: errOut},
		Caps:  output.Detect(in, out, env),
		Clock: clock.System{},
		Env:   env,
	}, args)
}
