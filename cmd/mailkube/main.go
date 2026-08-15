// Command mailkube is the Mailkube command-line interface.
package main

import (
	"context"
	"io"
	"os"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/signals"
)

// main is the only place in the program that ends the process.
//
// It is also the only place a signal handler is installed. The handler cancels the context every
// command receives rather than ending the process itself: a command that is midway through
// writing a capture file needs to finish that sentence, and os.Exit from a signal handler is
// what makes a flush "usually" happen.
func main() {
	ctx, stop := signals.WithCancel(context.Background())
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	// Uninstalled before the process ends rather than with defer, because os.Exit runs no
	// deferred call and a handler left installed is one that outlives the thing it protects.
	stop()

	os.Exit(code)
}

// run builds the real dependencies and hands them to the command tree.
//
// Everything below this point is injected, which is what makes every screen assertable: the
// streams, the clock and the terminal model all arrive from here, and a test supplies its own.
// The terminal is inspected once, here, and passed down: a command that re-detected colour
// support per line could disagree with itself mid-screen.
func run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	env := output.OSEnv()

	return cli.Run(ctx, &feature.Deps{
		IO:    &feature.IOStreams{In: in, Out: out, ErrOut: errOut},
		Caps:  output.Detect(in, out, env),
		Clock: clock.System{},
		Env:   env,
	}, args)
}
