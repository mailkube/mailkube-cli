package feature

import (
	"fmt"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/buildinfo"
	"github.com/mailkube/mailkube-cli/internal/kernel/clientfactory"
	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// Deps is what a feature is given to do its work.
//
// It is deliberately a struct of injected collaborators rather than a set of globals: everything
// a command touches that is not pure computation — the streams it writes to, the clock it reads,
// the environment it resolves settings from — arrives here, so a test can substitute all of it.
//
// The fields are set before any command runs; the methods below are the interface a feature
// actually uses, and a feature should prefer them to reaching for a field, because each one
// encodes a contract that would otherwise be re-decided per command.
type Deps struct {
	// IO carries the process's input and output streams.
	IO *IOStreams
	// Caps is what the terminal can do, resolved once at startup.
	Caps output.Caps
	// Format is the resolved output format for this invocation.
	Format output.Format
	// Clock is the only source of the current time.
	Clock clock.Clock
	// Env reads the process environment.
	Env output.Lookup
	// Globals are the parsed global flags.
	Globals *settings.Globals
	// Store owns the config file.
	Store *configstore.Store

	// config caches the parsed file, so a command reading settings twice does not read the
	// file twice and cannot observe it changing underneath itself mid-command.
	config *configstore.Config
	// prompter is kept rather than rebuilt per question: it buffers the input stream, and a
	// second prompter would start with an empty buffer and lose whatever the first read.
	prompter *output.Prompter
}

// Prepare finalises the dependencies once the global flags are parsed.
//
// It cannot happen at construction: the config path, the output format and whether colour is
// allowed are all decided by flags, and flags are not parsed until cobra runs. Everything that
// depends on one of them is resolved here, once, before any command body executes.
func (d *Deps) Prepare() error {
	if d.Globals == nil {
		d.Globals = &settings.Globals{}
	}
	if d.Env == nil {
		d.Env = output.MapEnv(nil)
	}
	if d.Clock == nil {
		d.Clock = clock.System{}
	}

	if d.Globals.NoColor {
		d.Caps.Color = false
	}

	format, err := output.Resolve(d.Globals.Output, d.Caps)
	if err != nil {
		return err
	}
	d.Format = format

	return d.openStore()
}

// openStore points the store at the config file this invocation should use.
//
// A store that was already supplied is left alone, which is what lets a test hand in a store
// over a temporary directory without also having to set the flag that would have produced it.
func (d *Deps) openStore() error {
	if d.Store != nil {
		return nil
	}

	path := d.Globals.ConfigPath
	if path == "" {
		if v, ok := d.Env(settings.EnvConfig); ok && v != "" {
			path = v
		}
	}
	if path == "" {
		def, err := configstore.DefaultPath()
		if err != nil {
			return errs.Configf("cannot determine the config directory: %v", err)
		}
		path = def
	}

	d.Store = configstore.New(path, d.Clock)
	return nil
}

// Config returns the parsed config file, reading it at most once per invocation.
func (d *Deps) Config() (*configstore.Config, error) {
	if d.config != nil {
		return d.config, nil
	}
	cfg, err := d.Store.Load()
	if err != nil {
		return nil, err
	}
	d.config = cfg
	return cfg, nil
}

// ForgetConfig drops the cached file, so the next read sees what was just written.
//
// A command that mutates the config and then reports the result needs this; nothing else should.
func (d *Deps) ForgetConfig() { d.config = nil }

// Settings resolves every setting for this invocation, with provenance.
func (d *Deps) Settings(o settings.Overrides) (settings.Resolved, error) {
	cfg, err := d.Config()
	if err != nil {
		return settings.Resolved{}, err
	}
	return settings.Resolve(*d.Globals, o, cfg, d.Env), nil
}

// Factory builds the SDK client factory from the resolved settings.
//
// It is the only path from settings to an SDK client, so the mapping — including the base-URL
// normalisation whose trailing slash is load-bearing — happens once.
func (d *Deps) Factory(r settings.Resolved) *clientfactory.Factory {
	return clientfactory.New(clientfactory.Settings{
		APIKey:  r.APIKey.Value,
		BaseURL: clientfactory.NormalizeBaseURL(r.BaseURL.Value),
		Timeout: duration(r.Timeout.Value),
		// The outgoing User-Agent names this tool as well as the SDK, so a request from the
		// CLI is distinguishable from one made by a program that embeds the same library.
		UserAgentSuffix: "mailkube-cli/" + buildinfo.Read().Version,
		Verbosity:       d.Globals.Verbosity,
		LogTo:           d.IO.ErrOut,
	})
}

// duration parses a resolved timeout, falling back to the default rather than failing.
//
// The value can only have come from a duration flag or from this package's own constant, so a
// parse failure here is not a user error to report but an impossible state to survive.
func duration(v string) time.Duration {
	d, err := time.ParseDuration(v)
	if err != nil {
		return settings.DefaultTimeout
	}
	return d
}

// Emit writes a view model to the success stream in the resolved format.
//
// This is the only way a command produces output, which is what keeps the human and machine
// forms two renderings of one value rather than two descriptions of one event. A projection
// beats the format: --jq asks for a value, not for a screen.
func (d *Deps) Emit(v any) error {
	if d.Globals.JQ != "" {
		return output.Project(d.IO.Out, d.Globals.JQ, v)
	}
	return output.Render(d.IO.Out, d.Format, d.Caps, v)
}

// Progress writes a line to the error stream, unless the user asked for quiet.
//
// Progress never goes to the success stream. That is the contract a script depends on: stdout
// carries the payload and nothing else, so a caller can pipe it into a parser.
func (d *Deps) Progress(format string, a ...any) {
	if d.Globals.Quiet {
		return
	}
	// A failure to write progress is not a failure of the command. The exit code still carries
	// the outcome, and there is nothing left to report a broken error stream with.
	_, _ = fmt.Fprintf(d.IO.ErrOut, format+"\n", a...)
}

// Confirm asks a yes/no question, or refuses to guess when nobody can answer.
func (d *Deps) Confirm(question string) (bool, error) {
	return d.Prompter().Confirm(question)
}

// Prompter is the one place questions are asked, shared so a command that asks twice keeps
// reading from the same buffered stream.
func (d *Deps) Prompter() *output.Prompter {
	if d.prompter == nil {
		d.prompter = output.NewPrompter(d.IO.In, d.IO.ErrOut, d.Caps, d.Globals.AssumeYes)
	}
	return d.prompter
}
