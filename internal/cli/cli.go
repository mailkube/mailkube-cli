// Package cli is the composition root: it builds the root command from the feature registry.
//
// This is the only package that knows the full set of features, and Registry is the only place
// that list is written down. Everything else — the help output, the diagnostics, the generated
// documentation — derives from it, so none of them can drift from what the binary actually does.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
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

	// A malformed command line is a usage error and exits 2. Classifying it here, where cobra
	// reports it, is what lets the exit-code mapping stay a mapping: the alternative is matching
	// on cobra's error text further down, which breaks silently on a library upgrade.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return errs.WithCode(errs.CodeUsage, err)
	})
	// Root accepts any arguments so that an unmatched one reaches rootRun. Left at cobra's
	// default, cobra rejects it first with an error carrying no category, which lands on the
	// "server error" default and tells the user to consider retrying a typo.
	root.Args = cobra.ArbitraryArgs
	root.RunE = rootRun

	for _, f := range Registry() {
		root.AddCommand(f.Command(deps))
	}
	return root
}

// rootRun handles an invocation that matched no subcommand.
//
// Root owning this is what keeps "unknown command" a usage error rather than a string cobra
// prints on its own, and it is where the curated answer for verbs this CLI deliberately does not
// have will live.
func rootRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	return errs.Usagef("unknown command %q for %q\nRun 'mailkube --help' for the command list.",
		args[0], cmd.CommandPath())
}
