// Package commands implements `mailkube commands`: the machine-readable command tree.
//
// It exists because three things need to know what commands and flags this binary has — the
// generated documentation pages, the agent skill, and any tooling built on top — and each of
// them writing its own list is how a reference ends up describing a version of a tool that no
// longer exists. The binary is the only thing that can answer this correctly, so it answers it.
package commands

import (
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// SchemaVersion is the version of the shape this command emits.
//
// It is here from the first release because this output is a contract with consumers outside
// this repository, and a consumer that cannot tell which shape it received has no way to handle
// a second one. Incrementing it is how a breaking change to the tree announces itself.
const SchemaVersion = 1

// Feature emits the command tree.
type Feature struct{}

// New returns the commands feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "commands" }

// HelpEntries implements feature.Listed.
//
// None: this is tooling, and listing it on the screen a first-time user reads would spend a line
// on something they will never run.
func (*Feature) HelpEntries() []feature.Entry { return nil }

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "commands",
		Short: "Emit the command tree as data",
		Args:  cobra.NoArgs,
		// Hidden rather than absent. It is a supported, versioned interface, but it answers a
		// question no human has, and a help screen is a curated list rather than an index.
		Hidden: true,
		RunE: func(c *cobra.Command, _ []string) error {
			return deps.Emit(Describe(c.Root()))
		},
	}
}

// TreeView is the whole command tree.
type TreeView struct {
	// SchemaVersion identifies the shape of this document.
	SchemaVersion int `json:"schemaVersion"`
	// Command is the root command, with everything beneath it.
	Command CommandView `json:"command"`
}

// RenderText implements output.TextRenderer.
//
// The text form is the invocation paths, one per line, which is what makes this usable from a
// shell without a JSON parser. The full detail is in the machine formats, where it belongs.
func (v TreeView) RenderText(_ output.Caps) []string {
	return paths(v.Command, "")
}

// CommandView is one command and its subtree.
type CommandView struct {
	// Name is the command's own name, without its parents.
	Name string `json:"name"`
	// Use is the usage line, including its arguments.
	Use string `json:"use"`
	// Short is the one-line description.
	Short string `json:"short"`
	// Long is the extended description, when there is one.
	Long string `json:"long,omitempty"`
	// Hidden reports whether the command is omitted from help.
	Hidden bool `json:"hidden,omitempty"`
	// Flags are the flags declared on this command, excluding inherited ones.
	Flags []FlagView `json:"flags,omitempty"`
	// Commands are the subcommands, sorted by name so the document is stable.
	Commands []CommandView `json:"commands,omitempty"`
}

// FlagView is one flag.
type FlagView struct {
	// Name is the long name, without the leading dashes.
	Name string `json:"name"`
	// Shorthand is the single-letter form, when there is one.
	Shorthand string `json:"shorthand,omitempty"`
	// Usage is the description shown in help.
	Usage string `json:"usage"`
	// Type is the value the flag takes.
	Type string `json:"type"`
	// Default is the value used when the flag is absent.
	Default string `json:"default,omitempty"`
}

// Describe walks a command tree into the emitted shape.
//
// Cobra's own help rendering is not reusable as data — it is text, formatted for a terminal —
// so this reads the same structures cobra reads and reports them as they are.
func Describe(cmd *cobra.Command) TreeView {
	return TreeView{SchemaVersion: SchemaVersion, Command: describe(cmd)}
}

// describe converts one command and recurses into its children.
func describe(cmd *cobra.Command) CommandView {
	view := CommandView{
		Name:   cmd.Name(),
		Use:    cmd.Use,
		Short:  cmd.Short,
		Long:   cmd.Long,
		Hidden: cmd.Hidden,
	}

	// Local flags only. Inherited ones are reported once, on the root, rather than repeated on
	// every command, which would triple the document and imply they were declared where they
	// are not.
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		view.Flags = append(view.Flags, FlagView{
			Name:      f.Name,
			Shorthand: f.Shorthand,
			Usage:     f.Usage,
			Type:      f.Value.Type(),
			Default:   f.DefValue,
		})
	})

	children := cmd.Commands()
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	for _, child := range children {
		view.Commands = append(view.Commands, describe(child))
	}
	return view
}

// paths lists every invocation in the tree, depth first.
func paths(cmd CommandView, prefix string) []string {
	invocation := cmd.Name
	if prefix != "" {
		invocation = prefix + " " + cmd.Name
	}

	lines := []string{invocation}
	for _, child := range cmd.Commands {
		lines = append(lines, paths(child, invocation)...)
	}
	return lines
}
