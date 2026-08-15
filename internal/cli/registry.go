package cli

import (
	"github.com/mailkube/mailkube-cli/internal/features/emails"
	"github.com/mailkube/mailkube-cli/internal/features/meta/auth"
	"github.com/mailkube/mailkube-cli/internal/features/meta/commands"
	"github.com/mailkube/mailkube-cli/internal/features/meta/completion"
	"github.com/mailkube/mailkube-cli/internal/features/meta/config"
	"github.com/mailkube/mailkube-cli/internal/features/meta/dashboard"
	"github.com/mailkube/mailkube-cli/internal/features/meta/doctor"
	mkerrors "github.com/mailkube/mailkube-cli/internal/features/meta/errors"
	"github.com/mailkube/mailkube-cli/internal/features/meta/skill"
	"github.com/mailkube/mailkube-cli/internal/features/meta/topic"
	"github.com/mailkube/mailkube-cli/internal/features/meta/version"
	"github.com/mailkube/mailkube-cli/internal/features/scheduled"
	"github.com/mailkube/mailkube-cli/internal/features/smtpcheck"
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
	// The credential feature is named rather than constructed inline because two entries share
	// it: `init` is the guided walk through the same primitives `auth login` exposes, and a
	// test that substitutes the verification seam must substitute it for both.
	credentials := auth.New()

	return []feature.Feature{
		emails.New(),
		scheduled.New(),
		smtpcheck.New(),
		auth.NewInit(credentials),
		credentials,
		config.New(),
		dashboard.New(),
		doctor.New(),
		mkerrors.New(),
		skill.New(),
		topic.New(),
		version.New(),
		completion.New(),
		commands.New(),
	}
}
