// Package errors implements `mailkube errors explain`.
//
// A failure is only useful if the reader can act on it. The server sends a machine-readable name
// and a sentence; this command supplies the rest — whether re-running could ever work, what to
// check, and what to do instead — for every name the platform documents and for the submission
// codes worth knowing by heart.
//
// It is offline by design. Explaining an error is exactly what someone does when a request has
// just failed, and needing a working connection to read the explanation would be the wrong
// dependency at the worst moment.
package errors

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
)

// dashboardURL is the catalogue's spelling of a dashboard link, so no page path is written twice.
func dashboardURL(path string) string { return routes.Dashboard(path) }

// Feature explains error names and submission codes.
type Feature struct{}

// New returns the errors feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "errors" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "errors explain",
		Summary:    "Explain a Mailkube error code",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "errors",
		Short: "Explain Mailkube errors",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.explainCmd(deps))
	return cmd
}

// explainCmd builds `errors explain`.
func (f *Feature) explainCmd(deps *feature.Deps) *cobra.Command {
	var list bool

	cmd := &cobra.Command{
		Use:   "explain [name|code]",
		Short: "Explain an error name or an SMTP code",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if list || len(args) == 0 {
				return deps.Emit(listView())
			}
			entry, err := lookup(args[0])
			if err != nil {
				return err
			}
			return deps.Emit(entry)
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list every name and code this command explains")
	return cmd
}

// lookup finds an entry by error name, by SMTP code, or by code and enhanced status together.
//
// Unknown input is a usage error rather than an invented explanation. The platform may know names
// this release does not, and a confident wrong answer about a failure is worse than none — but
// the failure itself still renders in full wherever it happened, with the server's own message.
func lookup(query string) (Entry, error) {
	wanted := normalise(query)

	for _, entry := range catalogue() {
		if normalise(entry.Name) == wanted {
			return entry, nil
		}
		if entry.Enhanced != "" && normalise(entry.Name+" "+entry.Enhanced) == wanted {
			return entry, nil
		}
	}
	return Entry{}, errs.Usagef(
		"no explanation for %q.\nRun `mailkube errors explain --list` to see every name and code.",
		query)
}

// normalise makes the lookup forgiving about the shapes a name is written in.
//
// Someone pasting from a log has "quota_exceeded"; someone reading prose has "quota exceeded";
// someone quoting a bounce has "535 5.7.8". All three mean one entry, and turning the third away
// on punctuation would make this command feel broken at the moment it is most needed.
func normalise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}
