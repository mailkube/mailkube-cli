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

// Deps is what a feature is given to do its work.
//
// It is deliberately a struct of interfaces rather than a global: everything a command touches
// that is not pure computation — the streams it writes to, the clock it reads — arrives here, so
// a test can substitute all of it. Fields are added as the kernel grows; a feature takes what it
// needs and ignores the rest.
type Deps struct {
	// IO carries the process's input and output streams.
	IO *IOStreams
}
