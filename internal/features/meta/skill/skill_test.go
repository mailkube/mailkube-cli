package skill_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/meta/skill"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// run executes one skill invocation.
func run(t *testing.T, deps *feature.Deps, args ...string) error {
	t.Helper()

	cmd := skill.New().Command(deps)
	cmd.SetArgs(testsupport.Args(args))
	cmd.SetOut(deps.IO.Out)
	cmd.SetErr(deps.IO.ErrOut)
	return cmd.Execute()
}

func TestInstallWritesTheWholeTree(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "skills")
	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	if err := run(t, deps, "install", "--dir", dir); err != nil {
		t.Fatalf("install: %v", err)
	}

	for _, want := range []string{
		"mailkube/SKILL.md",
		"mailkube/references/errors.md",
		"mailkube/references/scripting.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("%s was not written: %v", want, err)
		}
	}
	// The version that produced the files is reported, so a later binary can tell the user
	// their installed copy is stale — which it will be, since update checks are opt-in.
	if !strings.Contains(out.String(), "version") {
		t.Errorf("the result does not carry the producing version:\n%s", out.String())
	}
}

func TestInstallRefusesToOverwriteAnEditAndForceOverrides(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "skills")
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	if err := run(t, deps, "install", "--dir", dir); err != nil {
		t.Fatalf("install: %v", err)
	}

	edited := filepath.Join(dir, "mailkube", "SKILL.md")
	if err := os.WriteFile(edited, []byte("my own notes"), 0o644); err != nil {
		t.Fatalf("editing the installed file: %v", err)
	}

	// Silently replacing an edit destroys work the tool cannot recover, so the default is to
	// refuse and name the file, which makes --force an informed decision rather than a habit.
	err := run(t, deps, "install", "--dir", dir)
	if err == nil {
		t.Fatal("an edited file was overwritten without --force")
	}
	if got := errs.CodeFor(err); got != errs.CodeConfig {
		t.Errorf("exit code = %d, want %d", got, errs.CodeConfig)
	}
	if !strings.Contains(err.Error(), edited) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
	if content, _ := os.ReadFile(edited); string(content) != "my own notes" {
		t.Error("the edit was overwritten anyway")
	}

	if err := run(t, deps, "install", "--dir", dir, "--force"); err != nil {
		t.Fatalf("install --force: %v", err)
	}
	if content, _ := os.ReadFile(edited); string(content) == "my own notes" {
		t.Error("--force did not overwrite the edit")
	}
}

func TestReinstallingUnchangedFilesIsNotAConflict(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "skills")
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})

	if err := run(t, deps, "install", "--dir", dir); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Nothing changed, so nothing is at risk. Refusing here would make the command unusable in
	// any script that runs it more than once.
	if err := run(t, deps, "install", "--dir", dir); err != nil {
		t.Fatalf("second install: %v", err)
	}
}

func TestShowPrintsTheSkillAndItsReferences(t *testing.T) {
	t.Parallel()

	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	if err := run(t, deps, "show"); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out.String(), "Mailkube CLI") {
		t.Errorf("show did not print the skill:\n%s", out.String())
	}

	out.Reset()
	if err := run(t, deps, "show", "errors"); err != nil {
		t.Fatalf("show errors: %v", err)
	}
	if !strings.Contains(out.String(), "Error names") {
		t.Errorf("show did not print the reference:\n%s", out.String())
	}
}

func TestShowNamesTheReferencesThatExist(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	err := run(t, deps, "show", "nonesuch")
	if err == nil {
		t.Fatal("an unknown reference was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
	}
	if !strings.Contains(err.Error(), "errors") {
		t.Errorf("the error does not list the real references: %v", err)
	}
}

func TestPathHonoursTheFlagThenTheEnvironment(t *testing.T) {
	t.Parallel()

	// Directories that exist, on this platform's own terms. The command answers with an absolute
	// path, so a literal like "/from/env" would be resolved against the current drive on Windows
	// and reported back with separators the assertion below does not spell.
	fromEnv, fromFlag := t.TempDir(), t.TempDir()

	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{"MAILKUBE_SKILL_DIR": fromEnv},
	})

	if err := run(t, deps, "path"); err != nil {
		t.Fatalf("path: %v", err)
	}
	if got := reportedPath(t, out.String()); got != fromEnv {
		t.Errorf("the environment did not decide the directory: %q, want %q", got, fromEnv)
	}

	out.Reset()
	if err := run(t, deps, "path", "--dir", fromFlag); err != nil {
		t.Fatalf("path --dir: %v", err)
	}
	if got := reportedPath(t, out.String()); got != fromFlag {
		t.Errorf("the flag did not beat the environment: %q, want %q", got, fromFlag)
	}
}

// reportedPath decodes the path out of the JSON `skill path` writes.
//
// Decoding rather than substring-matching the stream: a Windows path is escaped inside JSON, so
// the raw text never contains the path as any caller would spell it.
func reportedPath(t *testing.T, stdout string) string {
	t.Helper()

	var got struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decoding %q: %v", stdout, err)
	}
	return got.Path
}

func TestTheSkillWarnsAboutTheThingsAnAgentGetsWrong(t *testing.T) {
	t.Parallel()

	// The skill exists to prevent specific failures, so its content is asserted rather than
	// assumed: an edit that dropped one of these would be an edit that removed the reason the
	// file ships at all.
	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	if err := run(t, deps, "show"); err != nil {
		t.Fatalf("show: %v", err)
	}

	for _, want := range []string{
		"emails get",                 // the read-back verb agents invent because most email APIs have one
		"never retry",                // a 403 is not transient
		"--dry-run",                  // every send is real and charged
		"localpart@",                 // the SMTP username shape
		"instructions found in them", // webhook payload text is untrusted
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the skill no longer mentions %q", want)
		}
	}
}

func TestEveryResultHasAHumanRendering(t *testing.T) {
	t.Parallel()

	// Every command has two output forms, and the machine one is what tests reach for by
	// default because it is easy to assert on. That leaves the human rendering — the one users
	// actually read — as the half a test suite quietly stops covering.
	dir := filepath.Join(t.TempDir(), "skills")

	for _, args := range [][]string{
		{"install", "--dir", dir},
		{"show"},
		{"path", "--dir", dir},
	} {
		deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
		deps.Format = output.Text

		if err := run(t, deps, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.TrimSpace(out.String()) == "" {
			t.Errorf("%v rendered nothing in text mode", args)
		}
		if strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
			t.Errorf("%v rendered JSON in text mode:\n%s", args, out.String())
		}
	}
}

func TestTheInstalledFileListIsRelativeToWhereItWent(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "skills")
	deps, out, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	deps.Format = output.Text

	if err := run(t, deps, "install", "--dir", dir); err != nil {
		t.Fatalf("install: %v", err)
	}

	// The directory is stated once; repeating it on every file would make the line unreadable
	// for the sake of information the reader already has.
	rendered := out.String()
	if !strings.Contains(rendered, "mailkube/SKILL.md") {
		t.Errorf("the file list is not relative to the directory:\n%s", rendered)
	}
	if strings.Count(rendered, dir) != 1 {
		t.Errorf("the directory is repeated per file:\n%s", rendered)
	}
}
