package cli

import (
	"github.com/mailkube/mailkube-cli/internal/features/meta/version"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// Registry is the complete list of features the binary exposes, and the entire wiring.
//
// Adding a capability is one line here plus one new directory under internal/features. Nothing
// else changes: there is no switch statement, no central command list, and no second place
// where the set of commands is written down.
//
// It is a function rather than a package-level slice so that each call builds fresh command
// state. Cobra commands hold parsed flag values, so a shared tree would leak one invocation's
// flags into the next — which is invisible in production, where the process runs once, and
// maddening in tests, where it does not.
func Registry() []feature.Feature {
	return []feature.Feature{
		version.New(),
	}
}
