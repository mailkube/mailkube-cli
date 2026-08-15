package dashboard_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/meta/dashboard"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// runCmd executes one of the feature's commands against buffered streams.
func runCmd(t *testing.T, args ...string) (out, errOut string, code errs.Code) {
	t.Helper()

	deps, outBuf, errBuf := testsupport.TestDeps(t, testsupport.TestOptions{
		Globals: &settings.Globals{Output: "text"},
	})

	// The commands are siblings at the root rather than a subtree, so the test runs them the
	// way the root does: by name, out of the set the feature contributes.
	wanted := args[0]
	for _, cmd := range dashboard.New().Commands(deps) {
		if cmd.Name() != wanted {
			continue
		}
		cmd.SetArgs(args[1:])
		cmd.SetOut(outBuf)
		cmd.SetErr(errBuf)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true

		code = errs.CodeOK
		if err := cmd.Execute(); err != nil {
			detail := errs.Describe(err)
			code = detail.Code
			errBuf.WriteString(strings.Join(errs.Render(detail, "x"), "\n"))
		}
		return outBuf.String(), errBuf.String(), code
	}

	t.Fatalf("no command named %q", wanted)
	return "", "", 0
}

func TestTheMapNamesEveryAreaAndWhereItLives(t *testing.T) {
	t.Parallel()

	out, _, code := runCmd(t, "dashboard")
	if code != errs.CodeOK {
		t.Fatalf("exit code = %d", code)
	}

	for _, want := range []string{"domains", "templates", "suppressions", "audience", "logs"} {
		if !strings.Contains(out, want) {
			t.Errorf("the map does not mention %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "https://app.mailkube.com/domain/setup") {
		t.Errorf("the map gives no address to go to:\n%s", out)
	}
}

func TestAGuessedCommandSaysWhereTheThingActuallyIs(t *testing.T) {
	t.Parallel()

	// "unknown command" answers a question the user did not ask. This answers the one behind
	// it, and exits as a usage error because the command line was still wrong.
	_, errOut, code := runCmd(t, "domains", "verify", "acme.com")
	if code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", code, errs.CodeUsage)
	}
	if !strings.Contains(errOut, "https://app.mailkube.com/domain/setup") {
		t.Errorf("the referral gives no address:\n%s", errOut)
	}
}

func TestAGuessedCommandAcceptsWhateverWasTypedAfterIt(t *testing.T) {
	t.Parallel()

	// Someone types `templates list --json`, not `templates`. Refusing on the argument count
	// or on an unknown flag would answer the wrong question, so nothing after the name is
	// parsed at all.
	for _, args := range [][]string{
		{"templates"},
		{"templates", "list"},
		{"templates", "list", "--json", "--nonsense"},
	} {
		_, errOut, code := runCmd(t, args...)
		if code != errs.CodeUsage {
			t.Errorf("%v exited %d, want %d", args, code, errs.CodeUsage)
		}
		if !strings.Contains(errOut, "dashboard") {
			t.Errorf("%v was not answered with a referral:\n%s", args, errOut)
		}
	}
}

func TestTheWordsPeopleReachForAllLand(t *testing.T) {
	t.Parallel()

	// contacts, segments and audience are one page under three names, and the whole value of
	// the table is that it answers the word the user chose rather than the word we chose.
	for _, name := range []string{"contacts", "segments", "audience"} {
		_, errOut, _ := runCmd(t, name)
		if !strings.Contains(errOut, "/domain/audience") {
			t.Errorf("%q did not resolve to the audience page:\n%s", name, errOut)
		}
	}
}

func TestTheRealWebhooksCommandIsNotShadowed(t *testing.T) {
	t.Parallel()

	// The dashboard owns webhook endpoints, but `webhooks` is a command this CLI will have.
	// Claiming the name here would shadow it, which is a different failure from not having it.
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	for _, cmd := range dashboard.New().Commands(deps) {
		if cmd.Name() == "webhooks" {
			t.Error("the dashboard feature claimed the webhooks command name")
		}
	}
}
