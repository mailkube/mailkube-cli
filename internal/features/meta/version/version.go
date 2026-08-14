// Package version implements `mailkube version`.
//
// It is also the reference feature module: the smallest complete example of the shape every
// other module follows — a New constructor, Name and Command, and a runner that returns an
// error rather than printing one or ending the process.
package version

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/buildinfo"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// Feature reports the versions of the CLI and the SDK it was built against.
type Feature struct{}

// New returns the version feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "version" }

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the CLI and SDK versions",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return f.run(deps, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the versions as JSON")
	return cmd
}

// run writes the version report to the success stream.
//
// There is no update check here and none anywhere else by default. A tool that contacts a
// release server every time it runs is a privacy problem and breaks in an air-gapped CI
// environment, so checking is something a user asks for, never something that happens to them.
func (*Feature) run(deps *feature.Deps, asJSON bool) error {
	info := buildinfo.Read()

	if asJSON {
		encoder := json.NewEncoder(deps.IO.Out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(info)
	}

	_, err := fmt.Fprintf(deps.IO.Out, "mailkube %s\n  sdk  mailkube-go %s\n  go   %s\n",
		info.Version, info.SDKVersion, info.GoVersion)
	return err
}
