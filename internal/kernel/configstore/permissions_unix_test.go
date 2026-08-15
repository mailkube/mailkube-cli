//go:build !windows

package configstore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// The permission assertion is POSIX-only by design: Go's Chmod on Windows toggles only the
// read-only attribute, so the same check there would fail on every machine, every time.

func TestTheWrittenFileIsPrivate(t *testing.T) {
	t.Parallel()

	// A nested path, so the directory is one this package creates rather than one the test
	// framework handed over: its permissions are the thing under test.
	s := configstore.New(filepath.Join(t.TempDir(), "mailkube", "config.toml"), clock.Testing())
	err := s.Update(func(c *configstore.Config) error {
		c.Profiles["default"] = configstore.Profile{APIKey: "mk_test"}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != configstore.FileMode {
		t.Errorf("mode = %o, want %o", perm, configstore.FileMode)
	}

	dir, err := os.Stat(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatalf("Stat the directory: %v", err)
	}
	if perm := dir.Mode().Perm() & 0o077; perm != 0 {
		t.Errorf("directory mode = %o, which lets others in", dir.Mode().Perm())
	}
}

// Writing a credential into a file others can read would be this package creating the exact
// problem it exists to prevent, so the write is refused rather than the mode silently corrected.
func TestWritingToALooseFileIsRefused(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("active_profile = \"default\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := configstore.New(path, clock.Testing())

	err := s.Update(func(c *configstore.Config) error {
		c.Profiles["default"] = configstore.Profile{APIKey: "mk_test"}
		return nil
	})
	if err == nil {
		t.Fatal("a credential was written into a world-readable file")
	}
	if code := errs.CodeFor(err); code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", code, errs.CodeConfig)
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}

// Reading is a warning where writing is a failure: a user whose file is too open still needs the
// commands that would tell them so.
func TestPermissionWarningReportsWithoutBlocking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mode  os.FileMode
		warns bool
	}{
		{"private", 0o600, false},
		{"group readable", 0o640, true},
		{"world readable", 0o644, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("active_profile = \"a\"\n"), tc.mode); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			s := configstore.New(path, clock.Testing())

			warning, err := s.PermissionWarning()
			if err != nil {
				t.Fatalf("PermissionWarning: %v", err)
			}
			if (warning != "") != tc.warns {
				t.Errorf("warning = %q, want a warning: %v", warning, tc.warns)
			}

			// Whatever the mode, the file still reads.
			if _, err := s.Load(); err != nil {
				t.Errorf("Load was blocked by a permission warning: %v", err)
			}
		})
	}
}

func TestPermissionWarningIsSilentWithNoFile(t *testing.T) {
	t.Parallel()

	warning, err := newStore(t).PermissionWarning()
	if err != nil {
		t.Fatalf("PermissionWarning: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q for a file that does not exist", warning)
	}
}
