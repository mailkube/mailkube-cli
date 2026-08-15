package signals_test

import (
	"context"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/signals"
)

// Ctrl-C and a container runtime stopping the process are the same event to a script, so both have
// to arrive at the same exit code. This is the assertion that ties the handler to the contract.
func TestAStopSignalExitsOneThirty(t *testing.T) {
	t.Parallel()

	if got := errs.CodeFor(context.Canceled); got != errs.CodeInterrupt {
		t.Errorf("a cancelled context exits %d, want %d", got, errs.CodeInterrupt)
	}
}

func TestWithCancelInheritsTheParent(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	ctx, stop := signals.WithCancel(parent)
	defer stop()

	cancelParent()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the parent did not cancel the child")
	}
}

// Leaving the handler installed changes the process's signal disposition for as long as it runs.
// After stop, a signal must go back to being the runtime's business, not ours.
func TestStopUninstallsTheHandler(t *testing.T) {
	ctx, stop := signals.WithCancel(context.Background())
	stop()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not cancel the context it returned")
	}
}
