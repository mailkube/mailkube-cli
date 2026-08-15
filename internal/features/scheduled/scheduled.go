// Package scheduled implements `mailkube scheduled-emails`.
//
// A send carrying a scheduling time is accepted but not delivered yet, and until it is due it
// lives in a collection that can be listed, inspected, rescheduled and cancelled — one email at a
// time, or a whole batch at once.
//
// This is the only collection the CLI reads back, and the reason it can is that these messages
// have not been sent. Once one is dispatched it leaves the collection, which is why `--status
// sent` is refused rather than returning nothing: the answer to "what happened to it" is a
// webhook event, not a query.
package scheduled

import (
	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// SchedulesFor and BatchesFor build the clients this feature works through.
//
// Two seams rather than one, because the two namespaces are two services on the SDK client and a
// test that fakes one usually has nothing to say about the other.
type (
	// SchedulesFor builds the single-email client.
	SchedulesFor func(deps *feature.Deps, r settings.Resolved) (ScheduleService, error)
	// BatchesFor builds the batch client.
	BatchesFor func(deps *feature.Deps, r settings.Resolved) (ports.BatchWriter, error)
)

// ScheduleService is what the single-email verbs need: the read half and the write half.
type ScheduleService interface {
	ports.ScheduleReader
	ports.ScheduleWriter
}

// Feature manages scheduled sends.
type Feature struct {
	// Schedules builds the single-email client. Nil means the real one.
	Schedules SchedulesFor
	// Batches builds the batch client. Nil means the real one.
	Batches BatchesFor
}

// New returns the scheduled-emails feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "scheduled-emails" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSend,
		Invocation: "scheduled-emails",
		Summary:    "List, reschedule and cancel scheduled sends",
	}}
}

// SurfaceMapping implements feature.Documented.
//
// It is what lets the parity gate check that every published operation is either reachable from a
// command or deliberately absent, without a hand-maintained list anywhere.
func (*Feature) SurfaceMapping() []string {
	return []string{
		"GET /scheduled-emails",
		"GET /scheduled-emails/{id}",
		"PATCH /scheduled-emails/{id}",
		"DELETE /scheduled-emails/{id}",
		"PATCH /scheduled-emails/batches/{id}",
		"DELETE /scheduled-emails/batches/{id}",
	}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use: "scheduled-emails",
		// The short alias is what people type; the long name is what the resource is
		// called, and the help text should agree with the API documentation.
		Aliases: []string{"scheduled"},
		Short:   "List, reschedule and cancel scheduled sends",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		f.listCmd(deps),
		f.getCmd(deps),
		f.updateCmd(deps),
		f.cancelCmd(deps),
		f.batchesCmd(deps),
	)
	return cmd
}

// schedules returns the single-email client, real unless a test substituted one.
func (f *Feature) schedules(deps *feature.Deps, r settings.Resolved) (ScheduleService, error) {
	if f.Schedules != nil {
		return f.Schedules(deps, r)
	}
	client, err := deps.Factory(r).Client()
	if err != nil {
		return nil, err
	}
	return client.ScheduledEmails, nil
}

// batches returns the batch client, real unless a test substituted one.
func (f *Feature) batches(deps *feature.Deps, r settings.Resolved) (ports.BatchWriter, error) {
	if f.Batches != nil {
		return f.Batches(deps, r)
	}
	client, err := deps.Factory(r).Client()
	if err != nil {
		return nil, err
	}
	return client.ScheduledEmails.Batches, nil
}

// resolve is the preamble every verb here shares: settings, then a credential check.
//
// The credential is checked before anything else so a missing one is reported as itself rather
// than surfacing later as a failed request, and so nothing is read or prompted for first.
func (f *Feature) resolve(deps *feature.Deps) (settings.Resolved, error) {
	resolved, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return settings.Resolved{}, err
	}
	return resolved, requireKey(resolved)
}
