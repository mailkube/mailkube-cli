package cli_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// TestScreens pins every screen this milestone renders.
//
// Golden files are the primary gate for anything a user sees: changing one is a reviewable diff
// rather than a surprise in a release. The specification describes these screens in prose, but
// prose is a wish until a program prints it, so these files are generated from the binary and
// the prose is reconciled against them, never the other way round.
func TestScreens(t *testing.T) {
	t.Parallel()

	// Paths appear in several of these screens, so every case runs against the same fixed
	// config path. Without that a golden would record a different temporary directory on every
	// run, which is the classic way a golden test becomes a test of the test harness.
	const fixedPath = "/tmp/mailkube-golden/config.toml"

	tests := []struct {
		name string
		args []string
		// signedIn writes a populated config before running, for the screens whose whole
		// subject is what happens when credentials exist.
		signedIn bool
		wantCode errs.Code
	}{
		{name: "root_signed_out", args: nil},
		{name: "root_signed_in", args: nil, signedIn: true},
		{name: "version", args: []string{"version", "-o", "text"}},
		{name: "topic_list", args: []string{"topic", "-o", "text"}},
		{name: "topic_exit_codes", args: []string{"topic", "exit-codes", "-o", "text"}},
		{name: "config_list_empty", args: []string{"config", "list", "-o", "text"}},
		{name: "config_list", args: []string{"config", "list", "-o", "text"}, signedIn: true},
		{name: "config_profile_list", args: []string{"config", "profile", "list", "-o", "text"}, signedIn: true},
		{name: "config_path", args: []string{"config", "path", "-o", "text"}},
		{name: "auth_status_empty", args: []string{"auth", "status", "-o", "text"}},
		{name: "auth_status", args: []string{"auth", "status", "-o", "text"}, signedIn: true},
		{name: "doctor_offline", args: []string{"doctor", "--offline", "-o", "text"}},
		{name: "commands_paths", args: []string{"commands", "-o", "text"}},

		{
			name:     "error_unknown_command",
			args:     []string{"emails"},
			wantCode: errs.CodeUsage,
		},
		{
			name:     "error_unknown_setting",
			args:     []string{"config", "get", "api_secret"},
			wantCode: errs.CodeUsage,
		},
		{
			name:     "error_bare_smtp_username",
			args:     []string{"config", "set", "smtp_user", "myapp01"},
			wantCode: errs.CodeValidation,
		},
		{
			name:     "error_password_as_argument",
			args:     []string{"config", "set", "smtp_password", "hunter2"},
			wantCode: errs.CodeUsage,
		},
		{
			// Refusing rather than guessing is the contract: a command that cannot ask and
			// has no answer must not invent one, and must name the flag that supplies it.
			name:     "error_cannot_prompt",
			args:     []string{"auth", "login"},
			wantCode: errs.CodeUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := testsupport.TestOptions{ConfigPath: fixedPath}
			if tc.signedIn {
				path, _ := configured(t)
				opts.ConfigPath = path
			}

			got := run(t, opts, tc.args...)
			if got.code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\n%s", got.code, tc.wantCode, got.errOut)
			}
			assertGolden(t, tc.name, withStablePaths(got, opts.ConfigPath, fixedPath))
		})
	}
}

// withStablePaths rewrites the temporary config path to a fixed one.
//
// The signed-in cases need a real file, which means a real temporary directory, which differs on
// every run and on every machine. Templating it here rather than avoiding paths in the output
// keeps the screens honest: the path is genuinely part of what these commands print.
func withStablePaths(got result, actual, stable string) result {
	if actual == stable {
		return got
	}
	got.out = strings.ReplaceAll(got.out, actual, stable)
	got.errOut = strings.ReplaceAll(got.errOut, actual, stable)
	return got
}
