package config_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/meta/config"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// run executes one config invocation against a temporary file.
func run(t *testing.T, deps *feature.Deps, args ...string) error {
	t.Helper()

	cmd := config.New().Command(deps)
	cmd.SetArgs(args)
	cmd.SetOut(deps.IO.Out)
	cmd.SetErr(deps.IO.ErrOut)
	return cmd.Execute()
}

func TestSetAndUnsetRoundTripThroughTheFile(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	if err := run(t, deps, "set", "smtp_host", "smtp.example"); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg, err := deps.Store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Profiles["default"].SMTP.Host != "smtp.example" {
		t.Error("the value was not written")
	}

	if err := run(t, deps, "unset", "smtp_host"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	cfg, err = deps.Store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Profiles["default"].SMTP.Host != "" {
		t.Error("the value survived unset")
	}
}

func TestEveryWriteIsValidated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		wants errs.Code
	}{
		{"a port that is not a number", []string{"set", "smtp_port", "smtp"}, errs.CodeValidation},
		{"a port outside the range", []string{"set", "smtp_port", "70000"}, errs.CodeValidation},
		{"a TLS mode that does not exist", []string{"set", "smtp_tls", "ssl"}, errs.CodeValidation},
		{"a username with no domain", []string{"set", "smtp_user", "app01"}, errs.CodeValidation},
		{"a key nobody defined", []string{"set", "colour", "blue"}, errs.CodeUsage},
		// An argument is visible in shell history and in the process list, which is exactly
		// the exposure the credential exists to prevent, so there is no way to pass it.
		{"a password as an argument", []string{"set", "smtp_password", "hunter2"}, errs.CodeUsage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
			err := run(t, deps, tc.args...)
			if err == nil {
				t.Fatal("the write was accepted")
			}
			if got := errs.CodeFor(err); got != tc.wants {
				t.Errorf("exit code = %d, want %d (%v)", got, tc.wants, err)
			}
		})
	}
}

func TestAValidPortIsStoredAsANumberAndAnAbsentOneIsNotStoredAtAll(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	// Writing the username alone must not leave `port = 0` in a file the user reads and edits:
	// zero is not a port, and presenting it as one implies somebody chose it.
	if err := run(t, deps, "set", "smtp_user", "app01@acme.com"); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg, err := deps.Store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Profiles["default"].SMTP.Port != nil {
		t.Errorf("an unset port was stored as %d", *cfg.Profiles["default"].SMTP.Port)
	}

	if err := run(t, deps, "set", "smtp_port", "587"); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg, _ = deps.Store.Load()
	if got := cfg.Profiles["default"].SMTP.Port; got == nil || *got != 587 {
		t.Error("the port was not stored")
	}
}

func TestListMasksTheCredentialAndNeverShortensTheBaseURL(t *testing.T) {
	t.Parallel()

	const key = "mk_j3k1a2b3c4d5f8a2"
	const url = "https://api.example.test/mta/v1/"

	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Format = output.Text
	if err := run(t, deps, "set", "api_key", key); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := run(t, deps, "set", "base_url", url); err != nil {
		t.Fatalf("set: %v", err)
	}
	out.Reset()

	if err := run(t, deps, "list"); err != nil {
		t.Fatalf("list: %v", err)
	}
	rendered := out.String()

	if strings.Contains(rendered, key) {
		t.Errorf("the whole key was printed:\n%s", rendered)
	}
	// The trailing slash is load-bearing: without it, reference resolution silently drops the
	// version segment. A value elided from the right hides exactly the character that matters.
	if !strings.Contains(rendered, url) {
		t.Errorf("the base URL was not shown in full:\n%s", rendered)
	}
}

func TestGetReportsProvenanceAlongsideTheValue(t *testing.T) {
	t.Parallel()

	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{"MAILKUBE_BASE_URL": "https://env.example/v1/"},
	})
	if err := run(t, deps, "get", "base_url"); err != nil {
		t.Fatalf("get: %v", err)
	}

	// The text form is the bare value, because that is what a script reads. The provenance is
	// in the machine form, where a caller that wants it can ask.
	if !strings.Contains(out.String(), "env MAILKUBE_BASE_URL") {
		t.Errorf("the JSON form does not report the source:\n%s", out.String())
	}
}

func TestSwitchingProfilesRequiresOneThatExists(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	// Creating a profile as a side effect of selecting it would leave the user pointed at an
	// empty one, wondering where their credentials went.
	err := run(t, deps, "profile", "use", "staging")
	if err == nil {
		t.Fatal("a profile that does not exist was selected")
	}
	if got := errs.CodeFor(err); got != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got, errs.CodeConfig)
	}
}

func TestDeletingAProfileNeedsAnAnswerAndTakesOnlyThatProfile(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	if err := run(t, deps, "set", "api_key", "mk_default_key"); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Not interactive and no -y: a destructive verb must refuse rather than guess, in either
	// direction. Assuming yes destroys credentials nobody approved; assuming no reads like the
	// deletion was attempted and declined.
	err := run(t, deps, "profile", "delete", "default")
	if err == nil {
		t.Fatal("a profile was deleted without confirmation")
	}
	if got := errs.CodeFor(err); got != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
	}

	cfg, _ := deps.Store.Load()
	if _, still := cfg.Profiles["default"]; !still {
		t.Error("the profile was deleted anyway")
	}
}

func TestDeletingAProfileThatDoesNotExistIsNotFound(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Globals.AssumeYes = true

	err := run(t, deps, "profile", "delete", "ghost")
	if got := errs.CodeFor(err); got != errs.CodeNotFound {
		t.Errorf("exit code = %d, want %d", got, errs.CodeNotFound)
	}
}

func TestDeletingTheActiveProfileClearsTheSelection(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Globals.AssumeYes = true

	if err := run(t, deps, "set", "api_key", "mk_key"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := run(t, deps, "profile", "delete", "default"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Leaving active_profile pointing at a profile that no longer exists would make every
	// later command resolve against nothing while reporting a name.
	cfg, _ := deps.Store.Load()
	if cfg.ActiveProfile != "" {
		t.Errorf("active_profile still names the deleted profile: %q", cfg.ActiveProfile)
	}
}

func TestAnEmptyProfileListingSaysWhatToDo(t *testing.T) {
	t.Parallel()

	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Format = output.Text

	if err := run(t, deps, "profile", "list"); err != nil {
		t.Fatalf("profile list: %v", err)
	}
	if !strings.Contains(out.String(), "mailkube init") {
		t.Errorf("an empty listing does not name the next step:\n%s", out.String())
	}
}

// TestEverySettableKeyReadsBackAndClears walks the whole key table rather than sampling it.
//
// The read, write and clear paths are three separate mappings from a key name onto a field, and
// a key added to one but not the others fails silently: `set` succeeds, `get` reports nothing,
// and `unset` leaves the value behind. Walking the table is what makes that a test failure.
func TestEverySettableKeyReadsBackAndClears(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key   string
		value string
		// hasDefault marks the keys that still resolve to something after being cleared,
		// because a built-in value takes over. Clearing them removes the override, not the
		// setting, and expecting an empty read for those would be expecting a broken CLI.
		hasDefault bool
	}{
		{key: "api_key", value: "mk_j3k1a2b3c4d5f8a2"},
		{key: "base_url", value: "https://api.example.test/mta/v1/", hasDefault: true},
		{key: "smtp_user", value: "app01@acme.com"},
		{key: "smtp_host", value: "smtp.example.test"},
		{key: "smtp_port", value: "587"},
		{key: "smtp_tls", value: "implicit"},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
			deps.Format = output.Text

			if err := run(t, deps, "set", tc.key, tc.value); err != nil {
				t.Fatalf("set: %v", err)
			}

			out.Reset()
			if err := run(t, deps, "get", tc.key); err != nil {
				t.Fatalf("get: %v", err)
			}
			// api_key reads back masked, which is the point of it being a secret, so the
			// assertion is that something was resolved rather than that it round-tripped.
			if strings.TrimSpace(out.String()) == "-" {
				t.Errorf("%s did not read back after being set", tc.key)
			}

			if err := run(t, deps, "unset", tc.key); err != nil {
				t.Fatalf("unset: %v", err)
			}
			out.Reset()
			if err := run(t, deps, "get", tc.key); err != nil {
				t.Fatalf("get after unset: %v", err)
			}
			cleared := strings.TrimSpace(out.String())
			switch {
			case tc.hasDefault && cleared == tc.value:
				t.Errorf("%s survived unset: %q", tc.key, cleared)
			case !tc.hasDefault && cleared != "-":
				t.Errorf("%s survived unset: %q", tc.key, cleared)
			}
		})
	}
}

func TestReadingAKeyNobodyDefinedIsAUsageError(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	for _, args := range [][]string{
		{"get", "colour"},
		{"unset", "colour"},
		{"get", "smtp_password"},
		{"unset", "smtp_password"},
	} {
		err := run(t, deps, args...)
		if got := errs.CodeFor(err); got != errs.CodeUsage {
			t.Errorf("%v: exit code = %d, want %d (%v)", args, got, errs.CodeUsage, err)
		}
	}
}

func TestTheBareCommandsShowTheirHelp(t *testing.T) {
	t.Parallel()

	// `config` and `config profile` are groupings rather than actions. Doing nothing would be
	// unhelpful and guessing a verb would be worse, so they describe what is underneath them.
	for _, args := range [][]string{nil, {"profile"}} {
		deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
		if err := run(t, deps, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out.String(), "Usage:") {
			t.Errorf("%v printed no help:\n%s", args, out.String())
		}
	}
}

func TestEveryResultHasAHumanRendering(t *testing.T) {
	t.Parallel()

	// The machine form is what tests reach for by default, which quietly leaves the rendering
	// users actually read as the uncovered half. These are the screens, so they are exercised.
	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Format = output.Text
	deps.Globals.AssumeYes = true

	for _, args := range [][]string{
		{"path"},
		{"set", "api_key", "mk_j3k1a2b3c4d5f8a2"},
		{"list"},
		{"get", "api_key"},
		{"profile", "list"},
		{"profile", "use", "default"},
		{"unset", "api_key"},
		{"profile", "delete", "default"},
	} {
		out.Reset()
		if err := run(t, deps, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		rendered := strings.TrimSpace(out.String())
		if rendered == "" {
			t.Errorf("%v rendered nothing in text mode", args)
		}
		if strings.HasPrefix(rendered, "{") {
			t.Errorf("%v rendered JSON in text mode:\n%s", args, rendered)
		}
	}
}
