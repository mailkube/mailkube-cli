package auth

// `mailkube init` is the guided face of this package, and lives in it rather than in a module of
// its own for a structural reason: a feature module may never import a sibling, and init is by
// design nothing but a walk through the primitives `auth login` exposes. Credentials are one
// capability with two entry points — a guided one for a person setting up a machine, and a
// direct one for a script, which should not be routed through a wizard to store a key.

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// InitFeature runs the guided setup.
type InitFeature struct {
	// auth is the credential feature this delegates to, held rather than constructed so a
	// test can substitute its probe seam.
	credentials *Feature
}

// NewInit returns the guided-setup feature, sharing the credential feature it delegates to
// so that a test substituting the verification seam substitutes it for both.
func NewInit(credentials *Feature) *InitFeature { return &InitFeature{credentials: credentials} }

// Name implements feature.Feature.
func (*InitFeature) Name() string { return "init" }

// HelpEntries implements feature.Listed.
func (*InitFeature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "init",
		Summary:    "Guided first-time setup",
	}}
}

// Command implements feature.Feature.
func (f *InitFeature) Command(deps *feature.Deps) *cobra.Command {
	var noVerify bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Guided first-time setup",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			view, err := f.run(c.Context(), deps, noVerify)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "store the credential without checking it")
	return cmd
}

// run walks the setup: the API key first, then SMTP if the user wants it.
//
// SMTP defaults to no because most users never need it, and a wizard that insists on configuring
// a credential the user does not have is one people learn to skip entirely.
func (f *InitFeature) run(ctx context.Context, deps *feature.Deps, noVerify bool) (SetupView, error) {
	deps.Progress("")
	deps.Progress("  Welcome to Mailkube.")
	deps.Progress("")

	api, err := f.credentials.LoginAPI(ctx, deps, "", noVerify)
	if err != nil {
		return SetupView{}, err
	}
	view := SetupView{API: api}
	// Reported as it happens rather than saved for the end. A wizard that asked the next
	// question before showing the result of the last one would have the user deciding whether
	// to add SMTP credentials without yet knowing whether their API key even worked.
	f.report(deps, api)

	wantSMTP, err := deps.Confirm("Add SMTP credentials too?")
	if err != nil {
		return SetupView{}, err
	}
	if !wantSMTP {
		view.SMTPSkipped = true
		return view, nil
	}

	smtp, err := f.credentials.LoginSMTP(deps, "")
	if err != nil {
		return SetupView{}, err
	}
	view.SMTP = &smtp
	f.report(deps, smtp)
	return view, nil
}

// report renders one completed step to the progress stream.
//
// Progress, not payload: the steps are what the person watching needs, and the payload is the
// summary of what was configured, which is what a script reads. -q silences the first and never
// the second.
func (f *InitFeature) report(deps *feature.Deps, step LoginView) {
	for _, line := range step.RenderText(deps.Caps) {
		deps.Progress("%s", line)
	}
}

// SetupView is what the guided run configured.
type SetupView struct {
	// API is the result of storing the REST credential.
	API LoginView `json:"api"`
	// SMTP is the result of storing the submission credential, absent when it was skipped.
	SMTP *LoginView `json:"smtp,omitempty"`
	// SMTPSkipped records that the question was asked and declined, which is different from
	// never having been asked.
	SMTPSkipped bool `json:"smtpSkipped"`
}

// RenderText implements output.TextRenderer.
//
// It ends with a command to run, and that command carries --dry-run. Every send from this tool
// is real and charged, so the guided path teaches the safe habit rather than spending the user's
// quota to say hello.
func (v SetupView) RenderText(_ output.Caps) []string {
	var lines []string
	if v.SMTPSkipped {
		lines = append(lines, "  Skipped. Add them later with `mailkube auth login --smtp`.")
	}

	return append(lines,
		"",
		"  You're set up. Try:",
		"",
		"    mailkube emails send --from you@your-domain --to you@example.com \\",
		"      --subject \"Hello from the CLI\" --text \"It works.\" --dry-run",
		"",
		"  Docs: https://docs.mailkube.com/cli",
	)
}
