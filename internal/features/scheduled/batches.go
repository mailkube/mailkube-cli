package scheduled

import (
	"context"
	"strconv"

	"github.com/spf13/cobra"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
)

// batchesCmd builds `scheduled-emails batches`.
func (f *Feature) batchesCmd(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "batches",
		Short: "Reschedule or cancel a whole batch",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.batchUpdateCmd(deps), f.batchCancelCmd(deps))
	return cmd
}

// batchUpdateCmd builds `scheduled-emails batches update <batch-id>`.
func (f *Feature) batchUpdateCmd(deps *feature.Deps) *cobra.Command {
	var at string

	cmd := &cobra.Command{
		Use:     "update <batch-id>",
		Aliases: []string{"reschedule"},
		Short:   "Reschedule every pending send in a batch",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return f.runBatchUpdate(c.Context(), deps, args[0], at)
		},
	}
	cmd.Flags().StringVar(&at, "at", "", "the new due time for every email in the batch")
	return cmd
}

// runBatchUpdate moves a whole batch.
func (f *Feature) runBatchUpdate(ctx context.Context, deps *feature.Deps, batchID, at string) error {
	if at == "" {
		return errs.Usagef("no new time: pass --at, for example --at +2h")
	}
	due, err := input.ParseAt(at, deps.Clock.Now())
	if err != nil {
		return err
	}

	client, err := f.batchClient(deps)
	if err != nil {
		return err
	}

	updated, err := client.Update(ctx, batchID, mailkube.ScheduledEmailBatchUpdateParams{ScheduledAt: due})
	if err != nil {
		return err
	}
	return deps.Emit(BatchView{
		BatchID:  updated.BatchID,
		Action:   "rescheduled",
		Count:    updated.RescheduledCount,
		DueAt:    updated.ScheduledAt,
		dueHuman: humanTime(updated.ScheduledAt),
	})
}

// batchCancelCmd builds `scheduled-emails batches cancel <batch-id>`.
func (f *Feature) batchCancelCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <batch-id>",
		Short: "Cancel every pending send in a batch",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return f.runBatchCancel(c.Context(), deps, args[0])
		},
	}
}

// runBatchCancel cancels a whole batch, having first counted what that means.
//
// The count comes from a filtered listing made before the question is asked, so the prompt says
// how many messages are at stake rather than asking about a label. Declining costs one read and
// cancels nothing, which is the right price for not guessing.
func (f *Feature) runBatchCancel(ctx context.Context, deps *feature.Deps, batchID string) error {
	resolved, err := f.resolve(deps)
	if err != nil {
		return err
	}
	schedules, err := f.schedules(deps, resolved)
	if err != nil {
		return err
	}

	pending, err := schedules.List(ctx, mailkube.ScheduledEmailListParams{
		BatchID: batchID, Status: []string{"scheduled"},
	})
	if err != nil {
		return err
	}

	confirmed, err := deps.Confirm(confirmBatch(pending.Pagination.TotalCount, batchID))
	if err != nil {
		return err
	}
	if !confirmed {
		return errs.Newf(errs.CodeConfig, "canceled; the batch is still scheduled")
	}

	batches, err := f.batches(deps, resolved)
	if err != nil {
		return err
	}
	canceled, err := batches.Cancel(ctx, batchID)
	if err != nil {
		return err
	}
	return deps.Emit(BatchView{
		BatchID: canceled.BatchID,
		Action:  "canceled",
		Count:   canceled.CanceledCount,
	})
}

// confirmBatch phrases the question in messages rather than in labels.
func confirmBatch(count int, batchID string) string {
	if count == 1 {
		return "Cancel 1 scheduled email in batch \"" + batchID + "\"?"
	}
	return "Cancel " + strconv.Itoa(count) + " scheduled emails in batch \"" + batchID + "\"?"
}

// batchClient is the preamble the batch verbs that need no listing share.
func (f *Feature) batchClient(deps *feature.Deps) (ports.BatchWriter, error) {
	resolved, err := f.resolve(deps)
	if err != nil {
		return nil, err
	}
	return f.batches(deps, resolved)
}
