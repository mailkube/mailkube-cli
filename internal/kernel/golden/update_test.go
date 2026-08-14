package golden

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUpdateRewritesTheGoldenFile exercises the regeneration path that `make golden` runs.
//
// It is an in-package test because flipping the flag is the only way to reach that branch, and
// the flag is unexported on purpose: a caller should regenerate through the documented command,
// not by toggling a knob from another package.
func TestUpdateRewritesTheGoldenFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	previous := *update
	*update = true
	t.Cleanup(func() { *update = previous })

	Assert(t, "nested/created.txt", []byte("written by -update\n"))

	written, err := os.ReadFile(filepath.Join("testdata", "golden", "nested", "created.txt"))
	if err != nil {
		t.Fatalf("-update did not create the golden file: %v", err)
	}
	// The trailing newline is preserved: the committed file is a record of what the program
	// actually printed, not a tidied version of it.
	if string(written) != "written by -update\n" {
		t.Errorf("golden content = %q, want the output verbatim", written)
	}

	// And the file it just wrote must be one it accepts, or regeneration produces a golden
	// that immediately fails.
	*update = false
	Assert(t, "nested/created.txt", []byte("written by -update\n"))
}

func TestAMissingGoldenFileFailsWithAnActionableMessage(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	fake := &testing.T{}
	Assert(fake, "does-not-exist.txt", []byte("anything"))
	if !fake.Failed() {
		t.Error("a missing golden file must fail rather than silently pass")
	}
}
