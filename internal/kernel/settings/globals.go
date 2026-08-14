package settings

import (
	"time"

	"github.com/spf13/pflag"
)

// Globals are the flags every command accepts.
//
// They live here rather than in the composition root because they are inputs to resolution, and
// a feature that needs to know whether the user forced a format should read the same struct the
// resolver read. Registering them is the root's job; owning their meaning is this package's.
type Globals struct {
	// Profile selects which set of stored credentials to use.
	Profile string
	// APIKey overrides the stored REST credential.
	APIKey string
	// BaseURL overrides the API root.
	BaseURL string
	// ConfigPath overrides where the config file is read from and written to.
	ConfigPath string
	// Timeout bounds a single API call. Zero means the default applies.
	Timeout time.Duration
	// AssumeYes answers every confirmation with yes, for non-interactive use.
	AssumeYes bool
	// Output forces an output format instead of inferring it from the terminal.
	Output string
	// JQ is a projection applied to the output value before it is written.
	JQ string
	// NoColor suppresses ANSI colour regardless of what the terminal supports.
	NoColor bool
	// Quiet suppresses progress and hints on the error stream. It never suppresses the
	// payload or an error report, and it never implies AssumeYes: silencing a tool and
	// authorising it to act unattended are different decisions.
	Quiet bool
	// Verbosity counts the -v flags: 1 is CLI progress, 2 adds SDK request logging.
	Verbosity int
}

// Register declares the global flags on a flag set, which is the root's persistent set.
//
// Every flag here is readable by every command, so the set is deliberately small: a flag that
// only one command honours but every command advertises is a promise the help text cannot keep.
func (g *Globals) Register(fs *pflag.FlagSet) {
	fs.StringVar(&g.Profile, "profile", "", "profile to use")
	fs.StringVar(&g.APIKey, "api-key", "", "API key, overriding the stored one")
	fs.StringVar(&g.BaseURL, "base-url", "", "API base URL, overriding the default")
	fs.StringVar(&g.ConfigPath, "config", "", "path to the config file")
	fs.DurationVar(&g.Timeout, "timeout", 0, "timeout for a single API call")
	fs.BoolVarP(&g.AssumeYes, "yes", "y", false, "answer every confirmation with yes")
	fs.StringVarP(&g.Output, "output", "o", "", "output format: text, json, ndjson or yaml")
	fs.StringVar(&g.JQ, "jq", "", "project the output through a jq expression")
	fs.BoolVar(&g.NoColor, "no-color", false, "never write ANSI colour")
	fs.BoolVarP(&g.Quiet, "quiet", "q", false, "suppress progress and hints on stderr")
	fs.CountVarP(&g.Verbosity, "verbose", "v", "verbose output; repeat for SDK request logging")
}
