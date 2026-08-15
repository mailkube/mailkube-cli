// Package feature defines the contract every CLI feature module implements.
//
// The CLI is organised as one feature module per capability, each owning a directory and
// contributing a command subtree. Adding a capability means writing a new module and adding one
// line to the registry; it never means editing an existing module, a switch statement, or a
// central list of commands.
//
// This package holds only the contract, so a feature depends on an interface rather than on the
// composition root that builds it.
package feature

import "github.com/spf13/cobra"

// Feature is one capability of the CLI: a named module that contributes a command subtree.
//
// Command receives the shared dependencies and returns a fully built subtree, so a feature owns
// its own flags and wiring and the root command owns none of them.
type Feature interface {
	// Name is the module's short identifier, matching its directory (for example "emails").
	Name() string
	// Command builds this feature's command subtree.
	Command(deps *Deps) *cobra.Command
}

// Multi is implemented by a feature contributing several top-level commands rather than one
// subtree.
//
// It exists for the one shape a subtree cannot express: a set of sibling commands that are
// siblings precisely because a user types them at the top level. The composition root prefers it
// when a feature offers it, so Command still returns the feature's own primary command and
// nothing is left dead.
type Multi interface {
	Feature
	// Commands returns every top-level command this feature contributes, including the one
	// Command returns.
	Commands(deps *Deps) []*cobra.Command
}

// Documented is implemented by a feature that maps onto the published API surface, so the parity
// gate can check that every endpoint is either reachable from a command or deliberately absent.
type Documented interface {
	// SurfaceMapping lists the API operations this feature covers.
	SurfaceMapping() []string
}

// Diagnosable is implemented by a feature that has something to report about the environment.
//
// It is what keeps `doctor` free of a hand-maintained list: doctor walks the registry, so a
// feature that can diagnose itself contributes its checks by existing.
type Diagnosable interface {
	// Checks returns this feature's diagnostics.
	Checks(deps *Deps) []Check
}

// Status is the verdict of one diagnostic.
type Status int

// The verdicts. A check reports at most one of them.
const (
	// StatusOK means the check passed.
	StatusOK Status = iota
	// StatusWarn means something is missing or unusual but nothing is broken.
	StatusWarn
	// StatusFail means the thing being checked does not work.
	StatusFail
)

// Check is one diagnostic `doctor` runs.
//
// Run returns the finding rather than printing it, so the same check feeds the human screen and
// the JSON report without either being a re-implementation of the other.
type Check struct {
	// Label is the short name of what is being checked, as the report's first column.
	Label string
	// Run performs the check and returns its verdict and a one-line detail.
	Run func() Finding
}

// Finding is what a Check reports.
type Finding struct {
	// Status is the verdict.
	Status Status `json:"status"`
	// Detail is the one-line explanation shown beside the label.
	Detail string `json:"detail"`
}
