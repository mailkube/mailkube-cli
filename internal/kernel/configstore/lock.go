package configstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// lockSuffix is appended to the config path to name the lock file.
const lockSuffix = ".lock"

// acquireTimeout bounds how long a mutation waits for another process to finish.
//
// The lock is held only around a read-modify-write of a small file, never across a prompt, so any
// wait longer than this means the holder is not making progress. Waiting forever would turn a
// crashed process into a permanently unusable CLI.
const acquireTimeout = 5 * time.Second

// acquireInterval is how often the wait re-checks.
const acquireInterval = 50 * time.Millisecond

// acquireAttempts bounds the wait by a count rather than by a deadline read from the clock.
//
// The clock is injected, and a test's clock does not advance, so a wall-clock deadline would make
// this loop unbounded in exactly the situation where a bug in it would be found.
const acquireAttempts = int(acquireTimeout / acquireInterval)

// staleAfter is the age at which a lock is assumed to be abandoned and broken.
//
// It is far longer than any legitimate hold, which takes milliseconds. The number is written down
// rather than left implicit because breaking a lock is the one operation here that can lose a
// concurrent write, and the condition under which it happens should be reviewable.
const staleAfter = 30 * time.Second

// lock takes the advisory lock guarding mutations, returning the function that releases it.
//
// Exclusive creation of a lock file is the mechanism because it is the one thing every platform
// and every filesystem agrees on. Advisory byte-range locks differ between operating systems and
// behave unpredictably over network filesystems, and a home directory on one of those is
// ordinary rather than exotic.
func (s *Store) lock() (release func(), err error) {
	path := s.path + lockSuffix
	if err := os.MkdirAll(filepath.Dir(path), DirMode); err != nil {
		return nil, errs.Configf("cannot create %s: %v", filepath.Dir(path), err)
	}

	for attempt := 0; attempt < s.attempts; attempt++ {
		token, err := s.tryLock(path)
		if err == nil {
			return func() { s.release(path, token) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, errs.Configf("cannot take the configuration lock %s: %v", path, err)
		}

		if s.breakIfStale(path) {
			continue
		}
		time.Sleep(s.interval)
	}

	return nil, errs.Configf(
		"another mailkube process is still writing %s.\nIf none is running, remove %s and try again.",
		s.path, path)
}

// tryLock creates the lock file exclusively, returning the token identifying this holder.
func (s *Store) tryLock(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, FileMode)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	// The contents are for a human who finds a stale lock and wants to know what left it, and
	// for the release check below. The timestamp is the clock's, so a test can age a lock
	// without sleeping.
	token := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(s.clock.Now().UnixNano(), 10)
	host, _ := os.Hostname()
	_, err = fmt.Fprintf(f, "pid %d\nhost %s\ntoken %s\n", os.Getpid(), host, token)
	return token, err
}

// release removes the lock file, but only if this holder still owns it.
//
// The token check is what stops a process whose lock was broken as stale from then deleting the
// lock a second process legitimately took. Without it, breaking a stale lock would create exactly
// the race that locking exists to prevent.
func (s *Store) release(path, token string) {
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "token "+token) {
		return
	}
	_ = os.Remove(path)
}

// breakIfStale removes a lock older than staleAfter, reporting whether it did.
func (s *Store) breakIfStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		// It went away on its own, which is the outcome we wanted anyway.
		return errors.Is(err, fs.ErrNotExist)
	}
	if s.clock.Now().Sub(info.ModTime()) < staleAfter {
		return false
	}
	return os.Remove(path) == nil
}
