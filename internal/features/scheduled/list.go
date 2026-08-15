package scheduled

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/clientfactory"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// listableStatuses are the statuses the collection can be filtered by.
//
// "sent" is deliberately not among them, and refusing it is more useful than passing it through:
// a dispatched message has left this collection, so the server would answer with an empty page
// and the user would read that as "nothing was sent".
func listableStatuses() []string { return []string{"scheduled", "canceled", "failed"} }

// itemCeiling bounds `--all` so a filter that matches far more than expected cannot walk for
// minutes without saying so. Hitting it is reported, never silent.
//
// It counts items rather than pages because the page size is the server's and this program does
// not know it: a ceiling expressed in pages would mean a different amount of work per release.
const itemCeiling = 1000

// listOptions is everything `scheduled-emails list` accepts.
type listOptions struct {
	status   []string
	batchID  string
	after    string
	before   string
	page     int
	all      bool
	maxItems int
}

// listCmd builds `scheduled-emails list`.
func (f *Feature) listCmd(deps *feature.Deps) *cobra.Command {
	o := &listOptions{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List scheduled sends",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return f.runList(c.Context(), deps, o)
		},
	}

	fs := cmd.Flags()
	fs.StringSliceVar(&o.status, "status", nil,
		"filter by status: "+strings.Join(listableStatuses(), ", ")+" (repeatable)")
	fs.StringVar(&o.batchID, "batch-id", "", "filter to one batch label")
	fs.StringVar(&o.after, "after", "", "only sends due at or after this time")
	fs.StringVar(&o.before, "before", "", "only sends due at or before this time")
	fs.IntVar(&o.page, "page", 0, "fetch one page by number")
	fs.BoolVar(&o.all, "all", false, "walk every page")
	// Named for what it does. It stops the iterator client-side and sends no query
	// parameter, where "--limit" would read like a server filter and there is none.
	fs.IntVar(&o.maxItems, "max-items", 0, "stop after this many items (client-side)")
	return cmd
}

// runList fetches one page, or every page.
func (f *Feature) runList(ctx context.Context, deps *feature.Deps, o *listOptions) error {
	params, err := o.params(deps)
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

	if o.all {
		return f.emitAll(ctx, deps, client, params, o.maxItems)
	}
	return f.emitPage(ctx, deps, client, params)
}

// params turns the flags into the SDK's filters, refusing the combinations that cannot mean
// anything.
func (o *listOptions) params(deps *feature.Deps) (mailkube.ScheduledEmailListParams, error) {
	var zero mailkube.ScheduledEmailListParams

	if o.all && o.page > 0 {
		return zero, errs.Usagef("--all walks every page, so it cannot be combined with --page")
	}
	if !o.all && o.maxItems > 0 {
		return zero, errs.Usagef("--max-items stops the --all walk, so it needs --all")
	}
	if err := checkStatuses(o.status); err != nil {
		return zero, err
	}

	after, err := optionalTime(o.after, "--after", deps)
	if err != nil {
		return zero, err
	}
	before, err := optionalTime(o.before, "--before", deps)
	if err != nil {
		return zero, err
	}

	return mailkube.ScheduledEmailListParams{
		Status:         o.status,
		BatchID:        o.batchID,
		ScheduledAtGTE: after,
		ScheduledAtLTE: before,
		Page:           o.page,
	}, nil
}

// checkStatuses refuses a status this collection cannot hold.
func checkStatuses(statuses []string) error {
	for _, status := range statuses {
		if slicesContains(listableStatuses(), status) {
			continue
		}
		if status == "sent" {
			return errs.Usagef(
				"--status sent has no results by design: a dispatched message leaves this collection.\n" +
					"Delivery outcomes arrive as webhook events. See `mailkube topic webhooks`.")
		}
		return errs.Usagef("unknown status %q: use one of %s",
			status, strings.Join(listableStatuses(), ", "))
	}
	return nil
}

// optionalTime parses a filter bound, naming the flag in any complaint.
func optionalTime(value, flag string, deps *feature.Deps) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	at, err := input.ParseAt(value, deps.Clock.Now())
	if err != nil {
		return time.Time{}, errs.Usagef("%s: %v", flag, err)
	}
	return at, nil
}

// emitPage renders one page, with the position in the collection.
func (f *Feature) emitPage(
	ctx context.Context, deps *feature.Deps, client ScheduleService, params mailkube.ScheduledEmailListParams,
) error {
	page, err := client.List(ctx, params)
	if err != nil {
		return err
	}
	return deps.Emit(pageView(page))
}

// emitAll walks every page, stopping at the caller's cap or at the ceiling.
//
// The iterator fetches lazily, so a `--max-items 5` over a thousand matches costs one page.
func (f *Feature) emitAll(
	ctx context.Context,
	deps *feature.Deps,
	client ScheduleService,
	params mailkube.ScheduledEmailListParams,
	maxItems int,
) error {
	limit := maxItems
	if limit <= 0 {
		limit = itemCeiling
	}

	var items []ItemView
	truncated := false

	for item, err := range client.All(ctx, params) {
		if err != nil {
			return err
		}
		if len(items) >= limit {
			truncated = true
			break
		}
		items = append(items, itemView(item))
	}

	if truncated {
		// Said out loud rather than silently cut. A listing that stopped early and looked
		// complete is how someone concludes a batch is smaller than it is.
		deps.Progress("Stopped after %d items. Raise --max-items to see more.", len(items))
	}
	return deps.Emit(ListView{Items: items, Complete: !truncated})
}

// requireKey reports a missing credential before any request is built.
func requireKey(r settings.Resolved) error {
	return clientfactory.RequireAPIKey(clientfactory.Settings{APIKey: r.APIKey.Value})
}

// slicesContains reports membership. Written out rather than pulled from a helper package
// because it is used once and the loop is shorter than the import.
func slicesContains(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
