//go:build !windows

package signals_test

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/signals"
)

// Sending a signal to the test process is the only honest way to check this: a fake would assert
// that the code calls a function, not that the process reacts.
func TestWithCancelCancelsOnAStopSignal(t *testing.T) {
	tests := []struct {
		name string
		sig  syscall.Signal
	}{
		{"interrupt", syscall.SIGINT},
		{"terminate", syscall.SIGTERM},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, stop := signals.WithCancel(context.Background())
			defer stop()

			if err := syscall.Kill(syscall.Getpid(), tc.sig); err != nil {
				t.Fatalf("Kill: %v", err)
			}

			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("the context was not cancelled")
			}

			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Errorf("ctx.Err() = %v, want context.Canceled", ctx.Err())
			}
		})
	}
}
