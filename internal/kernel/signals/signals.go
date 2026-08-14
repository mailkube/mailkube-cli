// Package signals is the only place this CLI installs a signal handler.
//
// Long-running commands need a stop that is orderly rather than abrupt: a capture file has to be
// flushed, in-flight work has to drain, and a summary has to print. Without a handler the process
// takes Go's default and dies immediately, which loses all three and reports an exit code no
// script was told to expect.
package signals

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// stopSignals are the two ways this process is asked to stop.
//
// SIGINT is Ctrl-C at a terminal and SIGTERM is a container runtime stopping the process. They are
// treated identically and produce the same exit code on purpose: to a script watching the outcome,
// "someone pressed Ctrl-C" and "the orchestrator stopped us" are the same event, and reporting
// them differently would mean writing two branches for one situation.
//
// os.Interrupt is listed separately because it is the portable spelling, and on Windows it is the
// only one of the three that arrives at all.
func stopSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// WithCancel returns a context cancelled when the process is asked to stop, and a function that
// uninstalls the handler.
//
// The returned stop function must be called, conventionally with defer: leaving the handler
// installed keeps the process's signal disposition changed for as long as it runs, which matters
// in tests, where many contexts are created inside one process.
//
// Cancellation, not exit: this package never ends the process. The context reaches whatever is
// waiting, that code winds itself up, and the error travels back to main like any other. Calling
// os.Exit from a signal handler is what makes a flush "usually" happen.
func WithCancel(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, stopSignals()...)
}
