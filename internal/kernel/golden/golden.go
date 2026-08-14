// Package golden compares command output against committed files.
//
// Golden files are this repository's primary correctness gate for anything a user sees: every
// screen the CLI prints is committed, so changing one shows up as a reviewable diff rather than
// as a surprise in a release. That only works if the comparison is deterministic, which is what
// this package is for.
//
// Two rules make the difference between a useful golden and a flaky one:
//
// Streams are captured separately. stdout carries the success payload and nothing else, and a
// golden that concatenated the two streams would let that contract break silently while staying
// green.
//
// Line endings are normalised. Git checks files out with CRLF on Windows unless told otherwise
// (.gitattributes marks testdata as binary), and a byte comparison against a CRLF checkout fails
// on every golden at once, for a reason that has nothing to do with the code.
package golden

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update reports whether golden files should be rewritten rather than compared.
//
// Regenerate with `go test ./... -update`, or `make golden`. Review the resulting diff: the
// point of a golden file is that a human sees the change, so a regeneration that is committed
// unread is worse than no golden at all.
//
//nolint:gochecknoglobals // the flag package must register this at init, before any test runs
var update = flag.Bool("update", false, "rewrite golden files instead of comparing against them")

// Assert compares got against testdata/golden/<name>, or rewrites it under -update.
//
// The name is a path relative to testdata/golden and may contain slashes, so a feature can group
// its screens (for example "version/plain").
// It takes testing.TB rather than *testing.T so the failure paths are themselves testable, and
// it reports with Errorf rather than Fatalf: a run that checks several screens should report
// every mismatch, not stop at the first.
func Assert(t testing.TB, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name)
	// Only line endings are normalised before writing. The committed file is otherwise exactly
	// what the program printed, trailing newline included — a golden that has been tidied is no
	// longer a record of real output.
	got = unixEndings(got)

	if *update {
		writeGolden(t, path, got)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("reading %s: %v\nrun `make golden` to create it, then review the diff", path, err)
		return
	}

	if !bytes.Equal(comparable(got), comparable(want)) {
		t.Errorf("output does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

// writeGolden creates the golden file and the directory holding it.
func writeGolden(t testing.TB, path string, content []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Errorf("creating the golden directory: %v", err)
		return
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Errorf("writing %s: %v", path, err)
	}
}

// unixEndings rewrites CRLF to LF.
//
// Git hands out CRLF on Windows unless told otherwise, and a byte comparison against such a
// checkout fails on every golden at once for a reason that has nothing to do with the code.
func unixEndings(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// comparable reduces content to the form two sides are compared in: unix endings, and no
// trailing blank lines, so an editor adding or removing a final newline is not a test failure.
func comparable(b []byte) []byte {
	return bytes.TrimRight(unixEndings(b), "\n")
}
