package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/meta/commands"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

func TestOutputIsJSONWhenTheStreamIsNotATerminal(t *testing.T) {
	t.Parallel()

	// The default terminal model in tests is a non-terminal, which is what a pipe, a subshell
	// and a CI runner all are. No flag was passed.
	got := run(t, testsupport.TestOptions{}, "config", "path")

	if !strings.HasPrefix(strings.TrimSpace(got.out), "{") {
		t.Errorf("output is not JSON: %q", got.out)
	}
}

func TestAProjectionBeatsTheFormatAndIsWrittenRaw(t *testing.T) {
	t.Parallel()

	// --jq asks for a value, not for a screen, so a string result is unquoted: the whole point
	// is that it can be captured straight into a shell variable.
	got := run(t, testsupport.TestOptions{}, "config", "path", "--jq", ".path")

	if strings.Contains(got.out, `"`) {
		t.Errorf("a projected string was quoted, so it cannot be captured cleanly: %q", got.out)
	}
	if !strings.Contains(got.out, "config.toml") {
		t.Errorf("output = %q", got.out)
	}
}

func TestEveryFormatRendersTheSameValue(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"text", "json", "ndjson", "yaml"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			got := run(t, testsupport.TestOptions{}, "version", "-o", format)
			if got.code != errs.CodeOK {
				t.Fatalf("exit code = %d: %s", got.code, got.errOut)
			}
			// Whatever the encoding, the same field is in there: these are renderings of
			// one value rather than descriptions of one event.
			if !strings.Contains(got.out, "go") {
				t.Errorf("%s output is missing the toolchain: %q", format, got.out)
			}
		})
	}
}

func TestAnUnknownFormatIsAUsageError(t *testing.T) {
	t.Parallel()

	got := run(t, testsupport.TestOptions{}, "version", "-o", "xml")
	if got.code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeUsage)
	}
}

func TestQuietSilencesProgressAndNeverThePayload(t *testing.T) {
	t.Parallel()

	got := run(t, testsupport.TestOptions{}, "config", "path", "-q")
	if got.out == "" {
		t.Error("-q suppressed the payload, which it must never do")
	}
	if got.errOut != "" {
		t.Errorf("-q left progress on stderr: %q", got.errOut)
	}
}

func TestQuietDoesNotSilenceAnErrorReport(t *testing.T) {
	t.Parallel()

	// Silencing a tool and hiding why it failed are different requests, and only the first was
	// made. A -q that swallowed the diagnosis would make a scripted failure unexplainable.
	got := run(t, testsupport.TestOptions{}, "topic", "nonesuch", "-q")
	if got.errOut == "" {
		t.Error("-q suppressed the error report")
	}
}

func TestTheGlobalFlagsReachEveryCommand(t *testing.T) {
	t.Parallel()

	// Registered once on the root's persistent set, so a new feature gets them by existing.
	// The alternative is every command declaring them, and the first one to forget --profile
	// silently reads the wrong credentials.
	for _, args := range [][]string{
		{"config", "path", "--profile", "other"},
		{"auth", "status", "--profile", "other"},
		{"doctor", "--offline", "--profile", "other"},
	} {
		got := run(t, testsupport.TestOptions{}, args...)
		if got.code != errs.CodeOK {
			t.Errorf("%v: exit code = %d: %s", args, got.code, got.errOut)
		}
	}
}

func TestTheConfigPathComesFromTheFlagThenTheEnvironment(t *testing.T) {
	t.Parallel()

	fromEnv := filepath.Join(t.TempDir(), "from-env.toml")
	// TestOptions.ConfigPath stands in for the flag, so setting both is the precedence case.
	got := run(t, testsupport.TestOptions{
		Env: map[string]string{settings.EnvConfig: fromEnv},
	}, "config", "path", "--jq", ".path")

	if strings.TrimSpace(got.out) != fromEnv {
		t.Errorf("config path = %q, want the one from %s", strings.TrimSpace(got.out), settings.EnvConfig)
	}
}

func TestTheCommandTreeIsVersionedAndComplete(t *testing.T) {
	t.Parallel()

	got := run(t, testsupport.TestOptions{}, "commands")
	if got.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", got.code, got.errOut)
	}

	var tree commands.TreeView
	if err := json.Unmarshal([]byte(got.out), &tree); err != nil {
		t.Fatalf("the command tree is not valid JSON: %v", err)
	}
	if tree.SchemaVersion != commands.SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", tree.SchemaVersion, commands.SchemaVersion)
	}

	// Every registered feature appears, because this document is what the generated
	// documentation and the agent skill are built from: a command missing here is a command
	// nothing downstream knows exists.
	found := map[string]bool{}
	for _, c := range tree.Command.Commands {
		found[c.Name] = true
	}
	for _, want := range []string{
		"auth", "commands", "completion", "config", "doctor", "emails", "init", "skill", "topic", "version",
	} {
		if !found[want] {
			t.Errorf("the command tree does not contain %q", want)
		}
	}

	// The global flags are reported once, on the root, rather than repeated on every command.
	names := map[string]bool{}
	for _, f := range tree.Command.Flags {
		names[f.Name] = true
	}
	for _, want := range []string{"profile", "api-key", "output", "jq", "quiet", "yes"} {
		if !names[want] {
			t.Errorf("the root does not declare the global flag --%s", want)
		}
	}
}

func TestCompletionIsGeneratedForEveryShellItClaims(t *testing.T) {
	t.Parallel()

	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			got := run(t, testsupport.TestOptions{}, "completion", shell)
			if got.code != errs.CodeOK {
				t.Fatalf("exit code = %d: %s", got.code, got.errOut)
			}
			// Written to the injected stream rather than the process's own, which is why
			// this command exists at all instead of cobra's built-in one.
			if !strings.Contains(got.out, "mailkube") {
				t.Errorf("the %s script does not mention the binary", shell)
			}
		})
	}

	if got := run(t, testsupport.TestOptions{}, "completion", "tcsh"); got.code != errs.CodeUsage {
		t.Errorf("an unsupported shell exited %d, want %d", got.code, errs.CodeUsage)
	}
}

func TestAnUnreadableConfigIsReportedAndNeverRepaired(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	const broken = "active_profile = \nthis is not toml\n"
	writeFile(t, path, broken)

	got := run(t, testsupport.TestOptions{ConfigPath: path}, "config", "list")
	if got.code != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got.code, errs.CodeConfig)
	}
	if !strings.Contains(got.errOut, path) {
		t.Errorf("the report does not name the file: %q", got.errOut)
	}

	// The file may hold the only copy of a credential, so a tool that "fixes" what it could not
	// parse is a tool that deletes one.
	if readFile(t, path) != broken {
		t.Error("an unparseable config file was rewritten")
	}

	// `config path` still works, because it is how a user finds the file they were just told
	// to repair.
	if got := run(t, testsupport.TestOptions{ConfigPath: path}, "config", "path"); got.code != errs.CodeOK {
		t.Errorf("config path failed on a broken file: %s", got.errOut)
	}
}
