// Package dashboard says where the parts of the product that are not in this CLI actually live.
//
// The CLI is the send path and the development loop. Domain setup, credentials, templates,
// suppressions and audiences are managed in the dashboard. Someone who reaches for one of them at
// the command line has made a reasonable guess, and "unknown command" answers a question they did
// not ask.
//
// So this contributes two things: a `dashboard` command listing where everything is, and one
// hidden command per capability someone might type, each explaining itself and pointing at the
// page that owns it.
package dashboard

import (
	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
)

// managedElsewhere are the command names the hidden commands answer.
//
// Deliberately not every area in the routes table: `webhooks` is a real command of this CLI, and
// claiming the name here would shadow it. This is the set of names someone might type that this
// tool will never implement, which is a different question from the set of areas that exist.
func managedElsewhere() []string {
	return []string{
		"domains", "api-keys", "templates", "suppressions", "contacts", "segments", "audience",
	}
}

// Feature points at what the dashboard owns.
type Feature struct{}

// New returns the dashboard feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "dashboard" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "dashboard",
		Summary:    "Show what is managed in the dashboard, and where",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Show what is managed in the dashboard, and where",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return deps.Emit(view())
		},
	}
}

// Commands implements feature.Multi: the listing command plus one answer per guessed name.
func (f *Feature) Commands(deps *feature.Deps) []*cobra.Command {
	commands := []*cobra.Command{f.Command(deps)}

	for _, name := range managedElsewhere() {
		area, ok := routes.AreaFor(name)
		if !ok {
			continue
		}
		commands = append(commands, referral(name, area))
	}
	return commands
}

// referral builds one command that exists only to explain that it is not here.
//
// Hidden, because this is an answer to a wrong guess rather than a capability to advertise:
// listing seven commands that all say "not here" would make the tool look like it does seven
// things it does not.
func referral(name string, area routes.Area) *cobra.Command {
	return &cobra.Command{
		Use:    name,
		Short:  area.Summary + ", managed in the dashboard",
		Hidden: true,
		// Arbitrary arguments, because someone types `domains verify acme.com` and
		// refusing on the argument count would answer the wrong question. Unknown flags are
		// tolerated for the same reason — `templates list --json` must reach the
		// explanation rather than die on a flag this command was never going to read.
		//
		// Tolerated rather than unparsed: turning flag parsing off entirely would also
		// swallow the global flags, so `-o text` would silently do nothing here while
		// working everywhere else.
		Args:               cobra.ArbitraryArgs,
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(_ *cobra.Command, _ []string) error {
			return routes.Refer(name, area)
		},
	}
}
