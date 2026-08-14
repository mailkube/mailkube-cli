// Package completion implements `mailkube completion`: shell completion scripts.
//
// The generators are cobra's, so this package is a command rather than an implementation. It
// exists as a feature module anyway, for one reason: cobra's built-in completion command writes
// to the process's own stdout, and every other command in this program writes to the injected
// streams. One command that bypassed the seam would be one command no golden file could cover.
package completion

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// Feature generates shell completion scripts.
type Feature struct{}

// New returns the completion feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "completion" }

// HelpEntries implements feature.Listed.
//
// None: it appears in the full command list, but the root screen is for the commands a new user
// needs, and completion is something you set up once and never think about again.
func (*Feature) HelpEntries() []feature.Entry { return nil }

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:       "completion <bash|zsh|fish|powershell>",
		Short:     "Generate a shell completion script",
		Long:      longHelp,
		Args:      cobra.ExactArgs(1),
		ValidArgs: shells(),
		RunE: func(c *cobra.Command, args []string) error {
			return generate(c.Root(), deps.IO.Out, args[0])
		},
	}
}

// shells is the set cobra can generate for.
func shells() []string { return []string{"bash", "zsh", "fish", "powershell"} }

// longHelp tells the user what to do with the output, which is the part that is not obvious.
const longHelp = `Generate a shell completion script.

The script is written to standard output, so where it goes is your shell's business:

  bash        mailkube completion bash > /etc/bash_completion.d/mailkube
  zsh         mailkube completion zsh > "${fpath[1]}/_mailkube"
  fish        mailkube completion fish > ~/.config/fish/completions/mailkube.fish
  powershell  mailkube completion powershell | Out-String | Invoke-Expression`

// generate writes the script for one shell to the given stream.
func generate(root *cobra.Command, out io.Writer, shell string) error {
	switch shell {
	case "bash":
		return root.GenBashCompletionV2(out, true)
	case "zsh":
		return root.GenZshCompletion(out)
	case "fish":
		return root.GenFishCompletion(out, true)
	case "powershell":
		return root.GenPowerShellCompletionWithDesc(out)
	default:
		return errs.Usagef("unsupported shell %q\nSupported: %s.", shell, "bash, zsh, fish, powershell")
	}
}
