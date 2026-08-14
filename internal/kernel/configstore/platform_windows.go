//go:build windows

package configstore

import "io/fs"

// insecureMode always reports no problem on Windows.
//
// Go's os.Chmod on Windows toggles only the read-only attribute, so a file created here can never
// report 0600 and the POSIX check would fail on every Windows machine, every time. That is worse
// than not checking: a diagnostic that always fails is one people learn to ignore, including on
// the platforms where it is telling the truth.
//
// The protection on Windows comes from the file's location. The configuration lives under the
// user's profile directory, which inherits an access control list granting the user and
// administrators only. `doctor` reports that inheritance rather than a mode.
func insecureMode(fs.FileMode) string { return "" }

// syncDir does nothing on Windows.
//
// A directory handle cannot be opened for synchronisation there. The rename itself is performed
// by MoveFileEx with a replace flag, which the platform documents as atomic, so the guarantee the
// POSIX path builds out of two steps is provided by the one call.
func syncDir(string) error { return nil }
