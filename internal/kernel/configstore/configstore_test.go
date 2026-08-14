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

// newStore returns a Store over a fresh temporary file, with the fixed clock every test shares.
func newStore(t *testing.T) *configstore.Store {
	t.Helper()
	return configstore.New(filepath.Join(t.TempDir(), "config.toml"), clock.Testing())
}

// A first run has no configuration. Reporting a missing file would send the user looking for one
// rather than at the credential they have not yet supplied.
func TestLoadTreatsAMissingFileAsEmpty(t *testing.T) {
	t.Parallel()

	cfg, err := newStore(t).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ActiveProfile != "" || len(cfg.Profiles) != 0 {
		t.Errorf("Load() = %+v, want an empty config", cfg)
	}
}

func TestUpdateRoundTripsEveryField(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	err := s.Update(func(c *configstore.Config) error {
		c.ActiveProfile = "default"
		c.Profiles["default"] = configstore.Profile{
			APIKey:  "mk_test",
			BaseURL: "https://api.example.com/mta/v1/",
			SMTP: &configstore.SMTP{
				Username: "app01@acme.com", Password: "secret",
				Host: "smtp.example.com", Port: 587, TLS: "starttls",
			},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := cfg.Profiles["default"]
	if cfg.ActiveProfile != "default" || got.APIKey != "mk_test" {
		t.Errorf("profile = %+v, active = %q", got, cfg.ActiveProfile)
	}
	if got.SMTP == nil || got.SMTP.Username != "app01@acme.com" || got.SMTP.Port != 587 {
		t.Errorf("smtp = %+v", got.SMTP)
	}
}

// The two credentials are independent principals, so a profile holding only one must round-trip
// without the other appearing as an empty stub.
func TestUpdateKeepsTheCredentialsIndependent(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	err := s.Update(func(c *configstore.Config) error {
		c.Profiles["default"] = configstore.Profile{APIKey: "mk_test"}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if smtp := cfg.Profiles["default"].SMTP; smtp != nil {
		t.Errorf("an SMTP credential appeared from nowhere: %+v", smtp)
	}
}

func TestUpdateAccumulatesAcrossCalls(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	for _, name := range []string{"default", "staging"} {
		err := s.Update(func(c *configstore.Config) error {
			c.Profiles[name] = configstore.Profile{APIKey: "mk_" + name}
			return nil
		})
		if err != nil {
			t.Fatalf("Update(%s): %v", name, err)
		}
	}

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Profiles) != 2 {
		t.Errorf("profiles = %+v, want both", cfg.Profiles)
	}
}

// The file may hold the only copy of a credential, so a tool that repairs what it cannot parse is
// a tool that deletes one.
func TestAnUnparseableFileIsReportedAndLeftAlone(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	broken := "active_profile = \"default\"\n[profiles.default\napi_key = \"mk_test\"\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := configstore.New(path, clock.Testing())

	_, err := s.Load()
	if err == nil {
		t.Fatal("a broken file was parsed")
	}
	if code := errs.CodeFor(err); code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", code, errs.CodeConfig)
	}
	// The position is what makes the message actionable: "invalid config" leaves a user to
	// find the problem by bisection.
	if !strings.Contains(err.Error(), "line 3") || !strings.Contains(err.Error(), path) {
		t.Errorf("the error names neither the place nor the file: %v", err)
	}

	// An Update over a file that cannot be read must fail before it writes anything.
	if err := s.Update(func(*configstore.Config) error { return nil }); err == nil {
		t.Fatal("Update rewrote a file it could not parse")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != broken {
		t.Errorf("the broken file was modified:\n%s", after)
	}
}

// A failure inside the callback must leave the file as it was: the caller decided not to make
// the change, and a half-applied one is worse than none.
func TestUpdateWritesNothingWhenTheChangeFails(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	sentinel := errs.Configf("no")

	if err := s.Update(func(*configstore.Config) error { return sentinel }); err == nil {
		t.Fatal("a failing change was applied")
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Errorf("the file was created anyway: %v", err)
	}
}

// A temporary file left behind would accumulate one per write, in a directory holding credentials.
func TestUpdateLeavesNoStrayFiles(t *testing.T) {
	t.Parallel()

	s := newStore(t)
	err := s.Update(func(c *configstore.Config) error {
		c.Profiles["default"] = configstore.Profile{APIKey: "mk_test"}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "config.toml" {
			t.Errorf("left behind %s", e.Name())
		}
	}
}
