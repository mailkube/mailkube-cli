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
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
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

	if deps.Globals == nil {
		deps.Globals = &settings.Globals{}
	}
	deps.Globals.Register(root.PersistentFlags())

	// Everything that depends on a flag — the config path, the output format, whether colour
	// is allowed — is resolved here, after parsing and before any command body runs. Doing it
	// per command would mean every command remembering to, and the first one to forget would
	// be the one that wrote colour into a pipe.
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		return deps.Prepare()
	}

	// Cobra ships its own `completion` command that writes to the process's own stdout. This
	// program has one, wired to the injected streams like every other command, so the built-in
	// is turned off rather than left to shadow it.
	root.CompletionOptions.DisableDefaultCmd = true

	installHelp(root, deps)

	for _, f := range Registry() {
		// A feature normally owns one subtree. The exception is a set of sibling commands
		// that are siblings because a user types them at the top level, which no subtree can
		// express; those features say so, and this is the one place that difference lands.
		if multi, ok := f.(feature.Multi); ok {
			root.AddCommand(multi.Commands(deps)...)
			continue
		}
		root.AddCommand(f.Command(deps))
	}

	// Argument validation happens inside cobra, which reports "accepts 1 arg(s), received 0" as
	// an ordinary error carrying no category. Left alone it falls through the exit-code chain to
	// the server-error default, so a typo would exit 8 and be reported as retryable. Tagging it
	// here, once, is what keeps the mapping a mapping rather than a match on cobra's wording.
	tagUsageErrors(root)
	return root
}

// tagUsageErrors marks every command's argument validation as a usage failure.
func tagUsageErrors(cmd *cobra.Command) {
	for _, child := range cmd.Commands() {
		tagUsageErrors(child)
	}

	validate := cmd.Args
	if validate == nil {
		return
	}
	cmd.Args = func(c *cobra.Command, args []string) error {
		return errs.WithCode(errs.CodeUsage, validate(c, args))
	}
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
	return errs.Usagef("%s", unknownCommand(args[0], cmd.CommandPath()))
}
