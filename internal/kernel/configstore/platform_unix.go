//go:build !windows

package configstore

import (
	"io/fs"
	"os"
)

// insecureMode reports what is wrong with a file's permissions, or an empty string.
//
// The file holds credentials, so anything readable or writable beyond its owner is a problem.
// This is the POSIX half of the check; Windows expresses the same requirement through an access
// control list and cannot be asked this question.
func insecureMode(mode fs.FileMode) string {
	switch {
	case mode.Perm()&0o077 == 0:
		return ""
	case mode.Perm()&0o007 != 0:
		return "is readable by every user on this machine"
	default:
		return "is readable by its group"
	}
}

// syncDir flushes a directory's own metadata, so a rename survives a crash.
//
// A rename is a directory update, and until the directory itself is flushed the new name can be
// lost even though the file it points at is fully written. This is the step that makes the
// atomic write actually durable rather than merely atomic.
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		// The write itself succeeded; failing the command because the durability barrier
		// could not be applied would turn a completed operation into a reported failure.
		return nil //nolint:nilerr // durability is best-effort, correctness is not
	}
	defer func() { _ = d.Close() }()

	_ = d.Sync()
	return nil
}
