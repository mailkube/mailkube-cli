package feature_test

import (
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

func TestTestStreamsCapturesTheTwoOutputStreamsSeparately(t *testing.T) {
	// Separate buffers are what let a test assert the stdout/stderr split. A single combined
	// buffer would stay green while the payload leaked onto stderr.
	streams, out, errOut := feature.TestStreams()

	if _, err := streams.Out.Write([]byte("payload")); err != nil {
		t.Fatalf("write to Out: %v", err)
	}
	if _, err := streams.ErrOut.Write([]byte("progress")); err != nil {
		t.Fatalf("write to ErrOut: %v", err)
	}

	if out.String() != "payload" {
		t.Errorf("Out = %q", out.String())
	}
	if errOut.String() != "progress" {
		t.Errorf("ErrOut = %q", errOut.String())
	}
}

func TestTestStreamsProvidesAReadableInput(t *testing.T) {
	streams, _, _ := feature.TestStreams()
	if streams.In == nil {
		t.Error("In is nil; a command reading stdin would panic rather than see empty input")
	}
}
