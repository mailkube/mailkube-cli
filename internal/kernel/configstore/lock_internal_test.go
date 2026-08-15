package configstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// These tests reach inside the package to shorten the lock's wait policy. The contended path is
// worth testing and the real policy waits five seconds for it, which is the whole reason the
// policy is a field.

// impatient returns a Store that gives up on a held lock almost immediately.
func impatient(t *testing.T, at time.Time) *Store {
	t.Helper()

	s := New(filepath.Join(t.TempDir(), "config.toml"), clock.Fixed{At: at})
	s.attempts = 2
	s.interval = time.Millisecond
	return s
}

func TestTheLockIsReleasedAfterAnUpdate(t *testing.T) {
	t.Parallel()

	s := impatient(t, clock.Testing().Now())
	if err := s.Update(func(*Config) error { return nil }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := os.Stat(s.path + lockSuffix); !os.IsNotExist(err) {
		t.Errorf("the lock outlived the update: %v", err)
	}
	// And a second update must be able to take it again.
	if err := s.Update(func(*Config) error { return nil }); err != nil {
		t.Errorf("second Update: %v", err)
	}
}

// The lock is released even when the change fails, or one bad call would wedge the file until a
// stale-break thirty seconds later.
func TestTheLockIsReleasedWhenTheChangeFails(t *testing.T) {
	t.Parallel()

	s := impatient(t, clock.Testing().Now())
	if err := s.Update(func(*Config) error { return errs.Configf("no") }); err == nil {
		t.Fatal("a failing change was applied")
	}

	if _, err := os.Stat(s.path + lockSuffix); !os.IsNotExist(err) {
		t.Errorf("the lock outlived a failed update: %v", err)
	}
}

func TestAHeldLockBlocksAndSaysWhat(t *testing.T) {
	t.Parallel()

	s := impatient(t, clock.Testing().Now())
	if err := os.MkdirAll(filepath.Dir(s.path), DirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A lock taken now, by something that has not finished.
	if err := os.WriteFile(s.path+lockSuffix, []byte("pid 1\ntoken other\n"), FileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := s.Update(func(*Config) error { return nil })
	if err == nil {
		t.Fatal("a held lock was ignored")
	}
	if code := errs.CodeFor(err); code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", code, errs.CodeConfig)
	}
	// The way out has to be in the message: the alternative is a user who deletes the config
	// rather than the lock.
	if !strings.Contains(err.Error(), lockSuffix) {
		t.Errorf("the error does not name the lock file: %v", err)
	}
	// And the config was not written behind the lock.
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Errorf("the config was written while locked: %v", err)
	}
}

// A crashed process leaves its lock behind. Without a stale break, one crash makes every future
// mutation fail forever.
func TestAnAbandonedLockIsBroken(t *testing.T) {
	t.Parallel()

	s := impatient(t, clock.Testing().Now())
	if err := os.MkdirAll(filepath.Dir(s.path), DirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	lockPath := s.path + lockSuffix
	if err := os.WriteFile(lockPath, []byte("pid 1\ntoken gone\n"), FileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Age it past the break threshold.
	old := s.clock.Now().Add(-staleAfter - time.Minute)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if err := s.Update(func(c *Config) error {
		c.ActiveProfile = "default"
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveProfile != "default" {
		t.Errorf("the update did not land: %+v", cfg)
	}
}

// Without the token check, a process whose lock was broken as stale would go on to delete the
// lock a second process legitimately holds — recreating the race locking exists to prevent.
func TestReleaseLeavesSomeoneElsesLockAlone(t *testing.T) {
	t.Parallel()

	s := impatient(t, clock.Testing().Now())
	if err := os.MkdirAll(filepath.Dir(s.path), DirMode); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	lockPath := s.path + lockSuffix
	if err := os.WriteFile(lockPath, []byte("pid 2\ntoken theirs\n"), FileMode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s.release(lockPath, "mine")

	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("another holder's lock was removed: %v", err)
	}
}
