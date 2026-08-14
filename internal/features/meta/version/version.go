// Package version implements `mailkube version`.
//
// It is also the reference feature module: the smallest complete example of the shape every
// other module follows — a New constructor, Name and Command, a runner that returns an error
// rather than printing one, and a view model that the output layer renders.
package version

import (
	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/buildinfo"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// Feature reports the versions of the CLI and the SDK it was built against.
type Feature struct{}

// New returns the version feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "version" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "version",
		Summary:    "Show the CLI and SDK versions",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the CLI and SDK versions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if asJSON {
				deps.Format = output.JSON
			}
			return deps.Emit(f.report())
		},
	}
	// A spelling of -o json that reads naturally on this one command. It sets the same
	// resolved format rather than taking a second encoding path, so the two cannot diverge.
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the versions as JSON")
	return cmd
}

// report reads what this binary is.
//
// There is no update check here and none anywhere else by default. A tool that contacts a
// release server every time it runs is a privacy problem and breaks in an air-gapped CI
// environment, so checking is something a user asks for, never something that happens to them.
func (*Feature) report() View {
	info := buildinfo.Read()
	return View{Version: info.Version, SDKVersion: info.SDKVersion, GoVersion: info.GoVersion}
}

// View is what `mailkube version` reports.
type View struct {
	// Version is the CLI's own version.
	Version string `json:"version"`
	// SDKVersion is the Mailkube SDK this binary was built against, and by construction the
	// same string the outgoing User-Agent carries.
	SDKVersion string `json:"sdkVersion"`
	// GoVersion is the toolchain that built it.
	GoVersion string `json:"goVersion"`
}

// RenderText implements output.TextRenderer.
func (v View) RenderText(_ output.Caps) []string {
	return []string{
		"mailkube " + v.Version,
		"  sdk  mailkube-go " + v.SDKVersion,
		"  go   " + v.GoVersion,
	}
}
