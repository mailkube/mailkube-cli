package scheduled

import (
	"context"

	"github.com/spf13/cobra"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
)

// getCmd builds `scheduled-emails get <id>`.
func (f *Feature) getCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one scheduled send",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			resolved, err := f.resolve(deps)
			if err != nil {
				return err
			}
			client, err := f.schedules(deps, resolved)
			if err != nil {
				return err
			}

			email, err := client.Get(c.Context(), args[0])
			if err != nil {
				return err
			}
			return deps.Emit(detailView(deps, email))
		},
	}
}

// updateCmd builds `scheduled-emails update <id>`.
func (f *Feature) updateCmd(deps *feature.Deps) *cobra.Command {
	var (
		at      string
		batchID string
	)

	cmd := &cobra.Command{
		Use: "update <id>",
		// The verb people reach for is "reschedule"; the verb the API uses is "update".
		// Both work, and the resource name is the primary so the help agrees with the docs.
		Aliases: []string{"reschedule"},
		Short:   "Reschedule one scheduled send",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return f.runUpdate(c.Context(), deps, args[0], at, batchID)
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "the new due time: RFC 3339 with an offset, or +2h")
	cmd.Flags().StringVar(&batchID, "batch-id", "", "move it into this batch at the same time")
	return cmd
}

// runUpdate reschedules one email.
func (f *Feature) runUpdate(ctx context.Context, deps *feature.Deps, id, at, batchID string) error {
	if at == "" {
		return errs.Usagef("no new time: pass --at, for example --at +2h")
	}
	due, err := input.ParseAt(at, deps.Clock.Now())
	if err != nil {
		return err
	}

	resolved, err := f.resolve(deps)
	if err != nil {
		return err
	}
	client, err := f.schedules(deps, resolved)
	if err != nil {
		return err
	}

	email, err := client.Update(ctx, id, mailkube.ScheduledEmailUpdateParams{
		ScheduledAt: due, BatchID: batchID,
	})
	if err != nil {
		return err
	}
	return deps.Emit(detailView(deps, email))
}

// cancelCmd builds `scheduled-emails cancel <id>`.
func (f *Feature) cancelCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel one scheduled send",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return f.runCancel(c.Context(), deps, args[0])
		},
	}
}

// runCancel cancels one email, after asking.
//
// Cancelling is not reversible — the message is not sent, and there is no un-cancel — so it is
// confirmed like every other destructive verb, and `-y` is what a script passes to say it meant
// it.
func (f *Feature) runCancel(ctx context.Context, deps *feature.Deps, id string) error {
	confirmed, err := deps.Confirm("Cancel scheduled send " + id + "?")
	if err != nil {
		return err
	}
	if !confirmed {
		// A declined confirmation is a failure of the command, not a success with nothing
		// done: a script that read exit 0 here would go on believing the send is cancelled.
		return errs.Newf(errs.CodeConfig, "canceled; the send is still scheduled")
	}

	resolved, err := f.resolve(deps)
	if err != nil {
		return err
	}
	client, err := f.schedules(deps, resolved)
	if err != nil {
		return err
	}

	canceled, err := client.Cancel(ctx, id)
	if err != nil {
		return err
	}
	return deps.Emit(CancelView{ID: canceled.ID, Status: canceled.Status})
}
