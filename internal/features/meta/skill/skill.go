// Package skill implements `mailkube skill`: the agent instructions, compiled into the binary.
//
// Coding agents drive this CLI, and they get a predictable set of things wrong: inventing a
// read-back verb because most email APIs have one, retrying a rejected credential, and treating
// a send in a test as free. Those are correctable with instructions, and instructions are only
// useful if they are present, so they ship inside the binary rather than beside it.
//
// Embedding is the decision that matters. Files shipped only in a release archive would be
// missing for anyone who installed with `go install`, and a container user would have to mount
// them. Embedded, they are available from every channel with no packaging special case.
package skill

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// tree is the skill, compiled into the binary.
//
//go:embed skills
var tree embed.FS

// root is the embedded directory the skill lives under.
const root = "skills"

// defaultDir is where `skill install` writes when nothing says otherwise.
const defaultDir = ".claude/skills"

// dirMode and fileMode are what installed files are created with. Nothing here is secret, so
// these are ordinary permissions rather than the config file's.
const (
	dirMode  os.FileMode = 0o755
	fileMode os.FileMode = 0o644
)

// Feature installs and prints the agent skill.
type Feature struct{}

// New returns the skill feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "skill" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "skill",
		Summary:    "Install the agent skill for coding assistants",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the agent skill for coding assistants",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.installCmd(deps), f.showCmd(deps), f.pathCmd(deps))
	return cmd
}

// installCmd writes the skill tree to disk.
func (f *Feature) installCmd(deps *feature.Deps) *cobra.Command {
	var (
		dir   string
		force bool
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the skill files to disk",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			view, err := f.install(deps, dir, force)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "where to write the skill; defaults to "+defaultDir)
	cmd.Flags().BoolVar(&force, "force", false, "overwrite files that differ from the embedded ones")
	return cmd
}

// install writes every embedded file, refusing to overwrite a file that differs.
//
// Refusing by default is the right way round: the skill is a file a user may have edited, and
// silently replacing an edit with the shipped version destroys work that the tool has no way to
// recover. Naming the files that differ makes --force an informed decision rather than a habit.
func (f *Feature) install(deps *feature.Deps, dir string, force bool) (InstallView, error) {
	target, err := installDir(deps, dir)
	if err != nil {
		return InstallView{}, err
	}

	files, err := embedded()
	if err != nil {
		return InstallView{}, err
	}

	view := InstallView{Dir: target, Version: deps.Build.Version}
	var conflicts []string

	for _, name := range files {
		content, readErr := tree.ReadFile(name)
		if readErr != nil {
			return InstallView{}, errs.Newf(errs.CodeInternal, "reading the embedded skill: %v", readErr)
		}

		path := filepath.Join(target, filepath.FromSlash(strings.TrimPrefix(name, root+"/")))
		differs, statErr := existsAndDiffers(path, content)
		if statErr != nil {
			return InstallView{}, statErr
		}
		if differs && !force {
			conflicts = append(conflicts, path)
			continue
		}
		if err := writeFile(path, content); err != nil {
			return InstallView{}, err
		}
		view.Written = append(view.Written, path)
	}

	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return InstallView{}, errs.Configf(
			"these files differ from the ones this binary carries:\n%s\nPass --force to overwrite them.",
			"  "+strings.Join(conflicts, "\n  "))
	}
	return view, nil
}

// installDir resolves where to write: the flag, the environment, or the default.
func installDir(deps *feature.Deps, dir string) (string, error) {
	if dir == "" {
		if v, ok := deps.Env(settings.EnvSkillDir); ok && v != "" {
			dir = v
		}
	}
	if dir == "" {
		dir = defaultDir
	}
	return filepath.Abs(dir)
}

// existsAndDiffers reports whether a file is present with different content.
func existsAndDiffers(path string, want []byte) (bool, error) {
	got, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errs.Configf("cannot read %s: %v", path, err)
	}
	return string(got) != string(want), nil
}

// writeFile creates a file and the directories above it.
func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return errs.Configf("cannot create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, fileMode); err != nil {
		return errs.Configf("cannot write %s: %v", path, err)
	}
	return nil
}

// showCmd prints the skill, or one of its references, without writing anything.
func (f *Feature) showCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "show [reference]",
		Short: "Print the skill, or one of its references",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			view, err := show(ref)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// show reads one embedded file by its short name.
func show(ref string) (ShowView, error) {
	name := embeddedPath("mailkube/SKILL.md")
	if ref != "" {
		name = embeddedPath("mailkube/references/" + strings.TrimSuffix(ref, ".md") + ".md")
	}

	content, err := tree.ReadFile(name)
	if err != nil {
		refs, _ := references()
		return ShowView{}, errs.Usagef("no skill reference named %q\nAvailable: %s.",
			ref, strings.Join(refs, ", "))
	}
	return ShowView{Body: string(content)}, nil
}

// pathCmd reports where install would write.
func (f *Feature) pathCmd(deps *feature.Deps) *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print where `skill install` would write",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			target, err := installDir(deps, dir)
			if err != nil {
				return err
			}
			return deps.Emit(PathView{Path: target})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "the directory to resolve instead of the default")
	return cmd
}

// embedded lists every file in the skill, sorted, so an install is reproducible.
func embedded() ([]string, error) {
	var files []string
	err := fs.WalkDir(tree, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		return nil, errs.Newf(errs.CodeInternal, "walking the embedded skill: %v", err)
	}
	sort.Strings(files)
	return files, nil
}

// references lists the loadable reference names.
func references() ([]string, error) {
	entries, err := fs.ReadDir(tree, embeddedPath("mailkube/references"))
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(names)
	return names, nil
}

// embeddedPath joins a name onto the embedded root, always with forward slashes: an embedded filesystem
// uses them on every platform, including the one where filepath does not.
func embeddedPath(name string) string { return root + "/" + name }

// InstallView reports what was written.
type InstallView struct {
	// Dir is where the files went.
	Dir string `json:"dir"`
	// Written are the files created or replaced.
	Written []string `json:"written"`
	// Version is the CLI version that produced them, so a later binary can tell the user
	// their installed copy is stale — which it will be, since update checks are opt-in.
	Version string `json:"version"`
}

// RenderText implements output.TextRenderer.
func (v InstallView) RenderText(caps output.Caps) []string {
	return []string{
		caps.Glyphs.OK + " Installed the mailkube skill (" + v.Version + ") to " + v.Dir,
		"  Files: " + strings.Join(shortNames(v.Written, v.Dir), ", "),
	}
}

// shortNames renders the written paths relative to their directory, so the line stays readable.
func shortNames(paths []string, dir string) []string {
	short := make([]string, 0, len(paths))
	for _, p := range paths {
		if rel, err := filepath.Rel(dir, p); err == nil {
			short = append(short, filepath.ToSlash(rel))
			continue
		}
		short = append(short, p)
	}
	return short
}

// ShowView is one skill document.
type ShowView struct {
	// Body is the whole file.
	Body string `json:"body"`
}

// RenderText implements output.TextRenderer.
func (v ShowView) RenderText(_ output.Caps) []string {
	return strings.Split(strings.TrimRight(v.Body, "\n"), "\n")
}

// PathView is where the skill would be installed.
type PathView struct {
	// Path is the resolved directory.
	Path string `json:"path"`
}

// RenderText implements output.TextRenderer.
func (v PathView) RenderText(_ output.Caps) []string { return []string{v.Path} }
