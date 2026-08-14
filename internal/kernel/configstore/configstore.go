// Package configstore owns the CLI's configuration file.
//
// It is a package rather than a pair of read and write helpers because the file has a lifecycle
// that has to be got right in one place: it holds credentials, so its permissions matter; two
// shells can write it at the same time, so mutations are serialised; and a crash during a write
// must not be able to destroy what was already there.
//
// A single 0600 file is the deliberate choice over an operating-system keychain. It is what
// kubectl, aws and gcloud do, it behaves identically on every platform, and it is inspectable by
// the person whose credentials it holds. A keychain backend would be four platform
// implementations for a marginal gain over correct file permissions.
package configstore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
)

// FileMode is the permission the config file is created with and required to keep.
const FileMode fs.FileMode = 0o600

// DirMode is the permission the config directory is created with.
const DirMode fs.FileMode = 0o700

// Config is the whole configuration file.
type Config struct {
	// ActiveProfile names the profile used when none is given. `config profile use` is the
	// only thing that writes it.
	ActiveProfile string `toml:"active_profile,omitempty"`
	// Profiles holds every configured profile by name.
	Profiles map[string]Profile `toml:"profiles,omitempty"`
}

// Profile is one set of credentials and settings.
//
// The two credentials are independent and either may be absent. They are different principals:
// the API key authenticates REST calls, and the SMTP pair authenticates submission for one
// sending domain. Neither substitutes for the other, and nothing here falls back from one to the
// other — a missing SMTP credential is an error, not a reason to quietly use the API instead.
type Profile struct {
	// APIKey authenticates the REST transport.
	APIKey string `toml:"api_key,omitempty"`
	// BaseURL overrides the SDK's default API endpoint.
	BaseURL string `toml:"base_url,omitempty"`
	// SMTP holds the submission credential, when one is configured.
	SMTP *SMTP `toml:"smtp,omitempty"`
}

// SMTP is the submission credential for one sending domain.
type SMTP struct {
	// Username is localpart@verified-domain. A bare name is not a valid username.
	Username string `toml:"username,omitempty"`
	// Password is the SMTP credential issued in the dashboard, not the API key.
	Password string `toml:"password,omitempty"`
	// Host is the submission host.
	Host string `toml:"host,omitempty"`
	// Port is the submission port, conventionally 587 for STARTTLS or 465 for implicit TLS.
	//
	// A pointer, so that "not configured" and "zero" are distinguishable in the file. The TOML
	// encoder's omitempty covers strings, maps and slices but not numbers, so a plain int would
	// write `port = 0` into a config a user is expected to read and edit by hand — a value that
	// is not a port, presented as though someone had chosen it.
	Port *int `toml:"port,omitempty"`
	// TLS is "starttls" or "implicit".
	TLS string `toml:"tls,omitempty"`
}

// Store reads and writes the configuration file at one path.
type Store struct {
	path  string
	clock clock.Clock
	// attempts and interval are the lock wait policy. They are fields rather than constants
	// so a test can exercise the contended path without waiting the real timeout out.
	attempts int
	interval time.Duration
}

// New returns a Store for the file at path.
//
// The path is resolved by the caller rather than here, because its precedence — the --config
// flag, then MAILKUBE_CONFIG, then the platform default — is the same precedence every other
// setting follows, and it belongs where the rest of it lives.
func New(path string, c clock.Clock) *Store {
	return &Store{path: path, clock: c, attempts: acquireAttempts, interval: acquireInterval}
}

// Path returns the file this Store reads and writes.
func (s *Store) Path() string { return s.path }

// DefaultPath is the platform's configuration location for this CLI.
//
// os.UserConfigDir resolves to ~/.config on Linux, ~/Library/Application Support on macOS and
// %AppData% on Windows, which is why no path literal appears anywhere in this repository and why
// every golden file templates the value rather than baking one in.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", errs.Configf("cannot find the user configuration directory: %v", err)
	}
	return filepath.Join(dir, "mailkube", "config.toml"), nil
}

// Load reads the configuration.
//
// A file that does not exist is not an error: a first run has no configuration, and every command
// that needs a credential reports the missing credential rather than the missing file, which is
// the thing the user can act on.
func (s *Store) Load() (*Config, error) {
	content, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return nil, errs.Configf("cannot read %s: %v", s.path, err)
	}

	cfg := &Config{}
	if err := toml.Unmarshal(content, cfg); err != nil {
		return nil, parseError(s.path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	return cfg, nil
}

// parseError reports an unreadable file, naming where it broke.
//
// The file is never rewritten or repaired. It may hold the only copy of a credential, and a tool
// that "fixes" a config it could not parse is a tool that deletes one — so the only thing on offer
// is an accurate description and the two ways out.
//
// The decoder's own message carries the line number, and it is passed through rather than
// summarised: "invalid configuration" tells a user nothing they can act on, and finding the
// problem by bisecting a file of credentials is not a reasonable thing to ask.
func parseError(path string, err error) error {
	return errs.Configf(
		"%s is not valid TOML: %v\nEdit the file, or move it aside and run `mailkube init`.",
		path, err)
}

// Update applies a change to the configuration, atomically and under a lock.
//
// It is the only supported way to mutate the file. Read-modify-write done by a caller is a race:
// `mailkube init` prompting in one shell while `auth login --smtp` runs in another is an ordinary
// thing to do, and the loser of that race would silently lose a credential.
func (s *Store) Update(apply func(*Config) error) error {
	release, err := s.lock()
	if err != nil {
		return err
	}
	defer release()

	cfg, err := s.Load()
	if err != nil {
		return err
	}
	if err := apply(cfg); err != nil {
		return err
	}
	return s.write(cfg)
}

// write encodes and atomically replaces the file.
func (s *Store) write(cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(s.path), DirMode); err != nil {
		return errs.Configf("cannot create %s: %v", filepath.Dir(s.path), err)
	}
	// An existing file with loose permissions is a hard failure on the way in, not a warning.
	// Writing a credential into a world-readable file would be this package creating the
	// problem it exists to prevent.
	if err := s.requireSecure(); err != nil {
		return err
	}

	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(cfg); err != nil {
		return errs.Newf(errs.CodeInternal, "cannot encode the configuration: %v", err)
	}

	return writeAtomic(s.path, encoded.Bytes())
}

// requireSecure refuses to write over a file whose permissions are wrong.
func (s *Store) requireSecure() error {
	info, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errs.Configf("cannot inspect %s: %v", s.path, err)
	}

	if problem := insecureMode(info.Mode()); problem != "" {
		return errs.Configf(
			"%s %s, and it holds credentials.\nFix it with `chmod 600 %s`, then try again.",
			s.path, problem, s.path)
	}
	return nil
}

// PermissionWarning describes a permission problem on the config file, or returns an empty string.
//
// Reading is a warning where writing is a failure. A user whose file is too open still needs their
// commands to run while they fix it, and refusing to read would lock them out of the tool that
// tells them what is wrong.
func (s *Store) PermissionWarning() (string, error) {
	info, err := os.Stat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errs.Configf("cannot inspect %s: %v", s.path, err)
	}

	if problem := insecureMode(info.Mode()); problem != "" {
		return s.path + " " + problem + " — it holds credentials. Fix it with `chmod 600`.", nil
	}
	return "", nil
}

// Port wraps a port number for storing in an SMTP block.
//
// A one-line helper because the field is a pointer, and `&587` is not valid Go: without this,
// every caller would need a throwaway variable, which is the kind of friction that eventually
// argues the field back into being a plain int and the zero port back into the file.
func Port(n int) *int { return &n }
