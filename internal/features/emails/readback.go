package emails

import (
	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
)

// readBackVerbs are the verbs people reach for expecting to look a sent message up.
func readBackVerbs() []string { return []string{"get", "list"} }

// readBackCmds builds a command per verb that does not exist, each explaining why.
//
// This is not a missing feature waiting to be written. Mailkube does not serve past state: once a
// message is dispatched it is gone from anything queryable, and what became of it is pushed as a
// webhook event instead. A user who does not know that will keep looking for the verb, so the
// verb exists and tells them.
//
// Hidden, because it is an answer rather than a capability, and it exits as a usage error: the
// command line was wrong, and nothing was attempted.
func (f *Feature) readBackCmds() []*cobra.Command {
	verbs := readBackVerbs()
	commands := make([]*cobra.Command, 0, len(verbs))

	for _, verb := range verbs {
		commands = append(commands, &cobra.Command{
			Use:    verb,
			Short:  "Not available — delivery outcomes arrive as webhook events",
			Hidden: true,
			// Unknown flags are tolerated rather than unparsed: someone types
			// `emails get <id> --json`, and turning parsing off would also swallow the
			// global flags, so -o would silently do nothing here alone.
			Args:               cobra.ArbitraryArgs,
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(_ *cobra.Command, _ []string) error {
				return errNoReadBack()
			},
		})
	}
	return commands
}

// errNoReadBack is the explanation, in one place because three surfaces quote it.
//
// It states the design rather than apologising for a gap, and it names both ways to observe a
// message: live, on your own machine, and after the fact, in the dashboard.
func errNoReadBack() error {
	logs, _ := routes.AreaFor("logs")

	return errs.Usagef(
		"Mailkube does not provide read-back of sent messages.\n\n"+
			"This is by design: the API does not serve past state. Delivery outcomes are\n"+
			"pushed as webhook events instead of being polled.\n\n"+
			"To observe what happened to a message:\n"+
			"  mailkube webhooks listen --public-url … --secret …     (locally, live)\n"+
			"  %s     (history)\n\n"+
			"Scheduled sends have not been dispatched yet, so those you can read:\n"+
			"  mailkube scheduled-emails list\n\n"+
			"See: mailkube topic webhooks",
		logs.URL())
}
