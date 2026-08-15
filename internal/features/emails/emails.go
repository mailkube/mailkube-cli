// Package emails implements `mailkube emails send`.
//
// It is the CLI's reason to exist: everything else here either sets up a send, explains one, or
// observes what became of it. Three properties shape the module.
//
// A send is built once and rendered twice. The payload a user edits, the payload `--dry-run`
// prints and the payload that goes on the wire are one value, so a preview cannot describe
// something other than what would be sent.
//
// Local validation covers what is certain and stops there. Shape, charset and mutual exclusion
// are decided here; anything that depends on the caller's plan is left to the server, because a
// refusal this program invented is one the user cannot appeal.
//
// And every send is real. There is no sandbox and no test key, so a message sent from here is
// charged and moves sender reputation. That is why `--dry-run` is prominent, and why the guided
// setup teaches it before it teaches anything else.
package emails

import (
	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// SenderFor builds the client a send runs against.
//
// It is a field on the feature rather than a call inside it, so a test can substitute a fake
// server without a network. The same seam is what keeps the send path honest in tests: nothing
// here reaches for a client it was not given.
type SenderFor func(deps *feature.Deps, r settings.Resolved) (ports.EmailSender, error)

// Feature sends mail.
type Feature struct {
	// Sender builds the send client. Nil means the real one.
	Sender SenderFor
}

// New returns the emails feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "emails" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSend,
		Invocation: "emails send",
		Summary:    "Send an email",
	}}
}

// SurfaceMapping implements feature.Documented.
//
// One operation, which is the whole of what this feature covers: a send. The read-back verbs this
// module answers are not operations, because there is no endpoint behind them by design.
func (*Feature) SurfaceMapping() []string {
	return []string{"POST /emails"}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "emails",
		Short: "Send email",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.sendCmd(deps))
	cmd.AddCommand(f.readBackCmds()...)
	return cmd
}

// sender returns the client a send runs against, real unless a test substituted one.
func (f *Feature) sender(deps *feature.Deps, r settings.Resolved) (ports.EmailSender, error) {
	if f.Sender != nil {
		return f.Sender(deps, r)
	}
	client, err := deps.Factory(r).Client()
	if err != nil {
		return nil, err
	}
	return client.Emails, nil
}
