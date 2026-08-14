// Package cli is the composition root: it builds the root command from the feature registry.
//
// This is the only package that knows the full set of features, and Registry is the only place
// that list is written down. Everything else — the help output, the diagnostics, the generated
// documentation — derives from it, so none of them can drift from what the binary actually does.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// NewRootCmd builds the root command and every feature subtree beneath it.
//
// It takes its dependencies rather than reaching for globals, and returns a fresh command tree
// on every call. That matters for tests: cobra commands carry parsed flag state, so a
// package-level root shared between tests leaks one test's flags into the next.
func NewRootCmd(deps *feature.Deps) *cobra.Command {
	root := &cobra.Command{
		Use:   "mailkube",
		Short: "Send mail, manage scheduled sends, and receive webhooks from your terminal",
		// The CLI reports its own errors, in its own format, on its own stream. Cobra's
		// defaults would print a usage dump after a failed API call, which buries the one
		// line the user needs.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(deps.IO.In)
	root.SetOut(deps.IO.Out)
	root.SetErr(deps.IO.ErrOut)

	for _, f := range Registry() {
		root.AddCommand(f.Command(deps))
	}
	return root
}
