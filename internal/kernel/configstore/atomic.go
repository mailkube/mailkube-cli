package configstore

import (
	"os"
	"path/filepath"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// writeAtomic replaces a file's contents in a way that cannot leave it half-written.
//
// The sequence is: write a temporary file in the same directory, flush it to disk, rename it over
// the target, then flush the directory itself. Each step is there for a failure the previous one
// does not cover.
//
// Same directory, because rename is only atomic within a filesystem: a temporary file in the
// system temp directory can land on a different one, and the rename then degrades to a copy that
// can be interrupted. Created at 0600 from the start, because a file that is briefly world
// readable is world readable — there is no window small enough to be safe for a credential.
// Flushed before the rename, because otherwise a crash can leave the new name pointing at
// unwritten blocks, which is worse than either version. And the directory is flushed after,
// because the rename itself is metadata and can outlive its own durability.
//
// What this buys: a crash at any point leaves either the previous file intact or the new one
// complete, and never a truncated file holding half a credential.
func writeAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return errs.Configf("cannot write in %s: %v", dir, err)
	}
	tmpName := tmp.Name()
	// Removing a temporary file that was successfully renamed is a no-op, so this needs no
	// condition; what it covers is every early return below.
	defer func() { _ = os.Remove(tmpName) }()

	if err := writeAndClose(tmp, content); err != nil {
		return errs.Configf("cannot write %s: %v", tmpName, err)
	}
	// CreateTemp makes the file 0600 already on POSIX; this states the requirement rather than
	// inheriting it, and is a no-op where the platform expresses permissions differently.
	if err := os.Chmod(tmpName, FileMode); err != nil {
		return errs.Configf("cannot set permissions on %s: %v", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return errs.Configf("cannot replace %s: %v", path, err)
	}
	return syncDir(dir)
}

// writeAndClose writes the content, flushes it to the device, and closes the file.
func writeAndClose(f *os.File, content []byte) error {
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
