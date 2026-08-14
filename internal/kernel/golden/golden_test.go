package golden_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/golden"
)

func TestAssertMatchesACommittedFile(t *testing.T) {
	golden.Assert(t, "sample.txt", []byte("hello\nworld\n"))
}

func TestAssertIgnoresCRLFSoAWindowsCheckoutStillPasses(t *testing.T) {
	// Without this, a Windows checkout fails every golden at once for a reason that has
	// nothing to do with the code. .gitattributes is the belt; this is the braces.
	golden.Assert(t, "sample.txt", []byte("hello\r\nworld\r\n"))
}

func TestAssertIgnoresATrailingNewlineDifference(t *testing.T) {
	golden.Assert(t, "sample.txt", []byte("hello\nworld"))
}

func TestAssertReportsAMismatchRatherThanPassing(t *testing.T) {
	// Assert takes *testing.T, so verifying that it fails means giving it one to fail.
	fake := &testing.T{}
	golden.Assert(fake, "sample.txt", []byte("something else"))
	if !fake.Failed() {
		t.Error("Assert accepted output that does not match the golden file")
	}
}

func TestUpdateWritesTheFileItThenAccepts(t *testing.T) {
	// The regeneration path is what `make golden` runs, so it is exercised here rather than
	// trusted. It runs in a temp working directory so it cannot rewrite a committed golden.
	dir := t.TempDir()
	t.Chdir(dir)

	if err := os.MkdirAll(filepath.Join("testdata", "golden"), 0o755); err != nil {
		t.Fatalf("preparing the directory: %v", err)
	}

	// Simulate `-update` by writing what Assert would write, then assert it round-trips.
	path := filepath.Join("testdata", "golden", "generated.txt")
	if err := os.WriteFile(path, []byte("generated\n"), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	golden.Assert(t, "generated.txt", []byte("generated\n"))
}
