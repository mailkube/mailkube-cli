package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// docsURL is where the full reference lives, and the last line of the root screen.
const docsURL = "https://docs.mailkube.com/cli"

// groupOrder is the order the headings appear in, which is the order a new user needs them: what
// the tool does, then how to develop against it, then how to configure it.
//
// A function rather than a package-level slice, because package-level mutable state is banned
// repository-wide and a slice is mutable however it is declared.
func groupOrder() []string {
	return []string{feature.GroupSend, feature.GroupDevelop, feature.GroupSetup}
}

// renderRootHelp writes the screen a user sees when they run the binary with no arguments.
//
// Cobra's default help is a flat alphabetical list, which answers "what commands exist" but not
// "what is this for". This one groups by intent and ends with the single next action, because
// the most common state for a reader of this screen is not being set up yet.
func renderRootHelp(w io.Writer, root *cobra.Command, deps *feature.Deps) {
	info := deps.Build

	// Errors are discarded throughout: this is help text on a stream the caller chose, and
	// there is no useful recovery from a closed pipe while printing it.
	_, _ = fmt.Fprintf(w, "mailkube %s — the Mailkube CLI\n\n", info.Version)
	_, _ = fmt.Fprintf(w, "  %s.\n\n", root.Short)
	_, _ = fmt.Fprintf(w, "USAGE\n  mailkube <command> [flags]\n")

	grouped := entriesByGroup()
	width := longestInvocation(grouped)
	for _, group := range groupOrder() {
		entries := grouped[group]
		if len(entries) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "\n%s\n", group)
		for _, e := range entries {
			_, _ = fmt.Fprintf(w, "  %-*s  %s\n", width, e.Invocation, e.Summary)
		}
	}

	_, _ = fmt.Fprintf(w, "\n  Run `mailkube <command> --help` for details.\n")
	_, _ = fmt.Fprintf(w, "  Docs: %s\n", docsURL)

	if hint := signInHint(deps); hint != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", hint)
	}
}

// entriesByGroup collects every feature's root-screen lines.
//
// A feature that has not chosen its own lines gets one built from its command, so registering a
// feature is enough to make it discoverable. The alternative — a curated list in this file —
// is the central list the whole registry design exists to avoid.
func entriesByGroup() map[string][]feature.Entry {
	grouped := map[string][]feature.Entry{}
	for _, f := range Registry() {
		for _, e := range entriesFor(f) {
			grouped[e.Group] = append(grouped[e.Group], e)
		}
	}
	return grouped
}

// entriesFor returns one feature's lines, chosen by the feature or derived from its command.
func entriesFor(f feature.Feature) []feature.Entry {
	if listed, ok := f.(feature.Listed); ok {
		return listed.HelpEntries()
	}
	cmd := f.Command(&feature.Deps{})
	return []feature.Entry{{Group: feature.GroupSetup, Invocation: cmd.Name(), Summary: cmd.Short}}
}

// longestInvocation is the width the description column aligns to.
func longestInvocation(grouped map[string][]feature.Entry) int {
	width := 0
	for _, entries := range grouped {
		for _, e := range entries {
			if len(e.Invocation) > width {
				width = len(e.Invocation)
			}
		}
	}
	return width
}

// signInHint is the closing line, and the reason this screen is not a static string.
//
// It is the one piece of state on it: a user who has not stored a credential is told the single
// command that fixes that, and a user who has is not told anything they already know.
func signInHint(deps *feature.Deps) string {
	if deps == nil || deps.Globals == nil || deps.Store == nil {
		return ""
	}
	r, err := deps.Settings(settings.Overrides{})
	// An unreadable config is reported by the command the user actually ran, not by the help
	// screen, which has no business failing.
	if err != nil || r.APIKey.Set() {
		return ""
	}
	return "You are not signed in. Run `mailkube init` to get started."
}

// installHelp replaces the root screen while leaving every subcommand with cobra's own help.
//
// Only the root screen is worth hand-writing: it is the one a reader arrives at knowing nothing.
// A per-command screen is a reference, and cobra's rendering of flags, arguments and subcommands
// is both complete and familiar, so replacing it would cost more than it gained.
func installHelp(root *cobra.Command, deps *feature.Deps) {
	inherited := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root {
			renderRootHelp(cmd.OutOrStdout(), cmd, deps)
			return
		}
		inherited(cmd, args)
	})
}

// unknownCommand is the error for a verb this CLI does not have.
//
// It names the closest thing to a next step rather than dumping usage, because a mistyped verb
// is a one-line problem and the full command list is one flag away.
func unknownCommand(name, path string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"unknown command %q for %q\nRun 'mailkube --help' for the command list.", name, path))
}
