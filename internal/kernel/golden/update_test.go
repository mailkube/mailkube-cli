package golden

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps -update away from this package's own tests.
//
// Every test below asserts what Assert does when it COMPARES, and several compare different
// content against the same committed fixture. Under -update those become writes, so running
// `make golden` would rewrite that fixture to whichever test ran last and then fail the others.
// The one test that needs the regeneration path turns the flag on for itself.
//
// Flags are parsed first because m.Run would otherwise parse them afterwards and put the
// command line's value back.
func TestMain(m *testing.M) {
	flag.Parse()
	*update = false
	os.Exit(m.Run())
}

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

// TestAssertReportsAMismatchRatherThanPassing lives in this file rather than beside the other
// comparison tests because it is only meaningful while -update is off, and the flag that
// guarantees that is unexported.
func TestAssertReportsAMismatchRatherThanPassing(t *testing.T) {
	// Assert takes *testing.T, so verifying that it fails means giving it one to fail.
	fake := &testing.T{}
	Assert(fake, "sample.txt", []byte("something else"))
	if !fake.Failed() {
		t.Error("Assert accepted output that does not match the golden file")
	}
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
