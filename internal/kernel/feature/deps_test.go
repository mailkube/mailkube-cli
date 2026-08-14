package feature_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// prepared builds dependencies the way the composition root does, with nothing preset.
func prepared(t *testing.T, globals *settings.Globals, env map[string]string) (*feature.Deps, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	deps := &feature.Deps{
		IO:      &feature.IOStreams{In: strings.NewReader(""), Out: out, ErrOut: errOut},
		Env:     output.MapEnv(env),
		Globals: globals,
	}
	if err := deps.Prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return deps, out, errOut
}

func TestPrepareResolvesTheFormatFromTheFlagOrTheTerminal(t *testing.T) {
	t.Parallel()

	// Nothing is a terminal here, which is what a pipe and a CI runner both are.
	inferred, _, _ := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml"}, nil)
	if inferred.Format != output.JSON {
		t.Errorf("format = %v, want JSON when the stream is not a terminal", inferred.Format)
	}

	forced, _, _ := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml", Output: "yaml"}, nil)
	if forced.Format != output.YAML {
		t.Errorf("format = %v, want YAML", forced.Format)
	}
}

func TestPrepareRejectsAFormatThatDoesNotExist(t *testing.T) {
	t.Parallel()

	deps := &feature.Deps{
		IO:      &feature.IOStreams{In: strings.NewReader(""), Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		Globals: &settings.Globals{ConfigPath: "/tmp/x.toml", Output: "xml"},
	}
	err := deps.Prepare()
	if code := errs.CodeFor(err); code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d (%v)", code, errs.CodeUsage, err)
	}
}

func TestNoColorOverridesWhateverTheTerminalSupports(t *testing.T) {
	t.Parallel()

	// The user's instruction beats the detection: a terminal that can draw colour is not a
	// reason to write it at someone who asked for none.
	deps := &feature.Deps{
		IO:      &feature.IOStreams{In: strings.NewReader(""), Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
		Caps:    output.Caps{Color: true, TTY: true, Glyphs: output.UnicodeGlyphs()},
		Globals: &settings.Globals{ConfigPath: "/tmp/x.toml", NoColor: true},
	}
	if err := deps.Prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if deps.Caps.Color {
		t.Error("--no-color left colour enabled")
	}
	// It is only colour that was refused: the terminal is still a terminal, so it still gets
	// human output rather than being demoted to JSON.
	if deps.Format != output.Text {
		t.Errorf("format = %v, want Text", deps.Format)
	}
}

func TestTheConfigPathFollowsTheFlagThenTheEnvironment(t *testing.T) {
	t.Parallel()

	fromFlag, _, _ := prepared(t,
		&settings.Globals{ConfigPath: "/from/flag.toml"},
		map[string]string{settings.EnvConfig: "/from/env.toml"})
	if fromFlag.Store.Path() != "/from/flag.toml" {
		t.Errorf("path = %q, want the flag's", fromFlag.Store.Path())
	}

	fromEnv, _, _ := prepared(t, &settings.Globals{},
		map[string]string{settings.EnvConfig: "/from/env.toml"})
	if fromEnv.Store.Path() != "/from/env.toml" {
		t.Errorf("path = %q, want the environment's", fromEnv.Store.Path())
	}
}

func TestPrepareSuppliesDefaultsForWhatWasNotInjected(t *testing.T) {
	t.Parallel()

	// A caller that supplies only the streams still gets a usable set: this is what lets a test
	// state the one thing it cares about instead of every collaborator.
	deps := &feature.Deps{
		IO: &feature.IOStreams{In: strings.NewReader(""), Out: &bytes.Buffer{}, ErrOut: &bytes.Buffer{}},
	}
	if err := deps.Prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if deps.Globals == nil || deps.Env == nil || deps.Clock == nil || deps.Store == nil {
		t.Errorf("prepare left a collaborator nil: %+v", deps)
	}
}

func TestTheFactoryCarriesTheResolvedSettingsToTheSDK(t *testing.T) {
	t.Parallel()

	deps, _, _ := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml"}, nil)

	// A base URL with no trailing slash is the trap: reference resolution would otherwise drop
	// the version segment, and the request would 404 with no clue why.
	factory := deps.Factory(settings.Resolved{
		APIKey:  settings.Value{Value: "mk_key"},
		BaseURL: settings.Value{Value: "https://api.example/mta/v1"},
		Timeout: settings.Value{Value: "5s"},
	})
	if _, err := factory.Client(); err != nil {
		t.Fatalf("building the client: %v", err)
	}
}

func TestAnUnparseableTimeoutFallsBackRatherThanFailing(t *testing.T) {
	t.Parallel()

	// The value can only have come from a duration flag or from this package's own constant, so
	// a parse failure is not a user error to report but an impossible state to survive.
	deps, _, _ := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml"}, nil)
	factory := deps.Factory(settings.Resolved{
		APIKey:  settings.Value{Value: "mk_key"},
		Timeout: settings.Value{Value: "not a duration"},
	})
	if _, err := factory.Client(); err != nil {
		t.Fatalf("building the client: %v", err)
	}
}

func TestProgressGoesToTheErrorStreamAndObeysQuiet(t *testing.T) {
	t.Parallel()

	loud, out, errOut := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml"}, nil)
	loud.Progress("checking %s", "something")
	if !strings.Contains(errOut.String(), "checking something") {
		t.Errorf("progress did not reach the error stream: %q", errOut.String())
	}
	// stdout carries the payload and nothing else, so a caller can pipe it into a parser.
	if out.Len() != 0 {
		t.Errorf("progress reached the payload stream: %q", out.String())
	}

	quiet, _, quietErr := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml", Quiet: true}, nil)
	quiet.Progress("checking %s", "something")
	if quietErr.Len() != 0 {
		t.Errorf("-q left progress on stderr: %q", quietErr.String())
	}
}

func TestTheConfigIsReadOnceAndCanBeForgotten(t *testing.T) {
	t.Parallel()

	deps, _, _ := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml"}, nil)

	first, err := deps.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	second, err := deps.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	// Caching is what stops a command that resolves settings twice observing the file changing
	// underneath itself mid-command.
	if first != second {
		t.Error("the config file was read twice in one invocation")
	}

	deps.ForgetConfig()
	third, err := deps.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if third == first {
		t.Error("ForgetConfig did not force a re-read")
	}
}

func TestConfirmDefersToTheSharedPrompter(t *testing.T) {
	t.Parallel()

	yes, _, _ := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml", AssumeYes: true}, nil)
	ok, err := yes.Confirm("delete everything?")
	if err != nil || !ok {
		t.Errorf("-y did not answer the question: %v / %v", ok, err)
	}

	// The prompter is shared rather than rebuilt per question, because it buffers the input
	// stream: a second one would start empty and lose whatever the first read past the newline.
	first, second := yes.Prompter(), yes.Prompter()
	if first != second {
		t.Error("each question built a new prompter")
	}
}

func TestEmitRendersTheResolvedFormatAndAProjectionBeatsIt(t *testing.T) {
	t.Parallel()

	type payload struct {
		ID string `json:"id"`
	}

	asJSON, out, _ := prepared(t, &settings.Globals{ConfigPath: "/tmp/x.toml"}, nil)
	if err := asJSON.Emit(payload{ID: "9f3b2c14"}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(out.String(), `"id"`) {
		t.Errorf("output = %q", out.String())
	}

	projected, projectedOut, _ := prepared(t,
		&settings.Globals{ConfigPath: "/tmp/x.toml", JQ: ".id"}, nil)
	if err := projected.Emit(payload{ID: "9f3b2c14"}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	// A string result is written raw, so it can be captured straight into a shell variable.
	if strings.TrimSpace(projectedOut.String()) != "9f3b2c14" {
		t.Errorf("projection = %q", projectedOut.String())
	}
}

func TestTimeoutResolutionSurvivesADurationFlag(t *testing.T) {
	t.Parallel()

	deps, _, _ := prepared(t,
		&settings.Globals{ConfigPath: "/tmp/x.toml", Timeout: 90 * time.Second}, nil)

	got, err := deps.Settings(settings.Overrides{})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if got.Timeout.Value != "1m30s" {
		t.Errorf("timeout = %q, want 1m30s", got.Timeout.Value)
	}
}
