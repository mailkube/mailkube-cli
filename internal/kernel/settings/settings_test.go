package settings_test

import (
	"testing"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// stored is a config file with one fully populated profile plus a second, empty one.
func stored() *configstore.Config {
	return &configstore.Config{
		ActiveProfile: "work",
		Profiles: map[string]configstore.Profile{
			"work": {
				APIKey:  "mk_from_file",
				BaseURL: "https://file.example/v1/",
				SMTP: &configstore.SMTP{
					Username: "app01@acme.com",
					Password: "file-password",
					Host:     "smtp.example",
					Port:     configstore.Port(587),
					TLS:      "starttls",
				},
			},
			"empty": {},
		},
	}
}

// TestPrecedence is the whole contract of this package: flag beats environment beats file beats
// default, one rule, applied identically to every setting.
func TestPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		globals    settings.Globals
		env        map[string]string
		wantKey    string
		wantSource settings.Source
		wantLabel  string
	}{
		{
			name:       "the flag wins over everything",
			globals:    settings.Globals{APIKey: "mk_from_flag"},
			env:        map[string]string{settings.EnvAPIKey: "mk_from_env"},
			wantKey:    "mk_from_flag",
			wantSource: settings.FromFlag,
			wantLabel:  "flag --api-key",
		},
		{
			name:       "the environment wins over the file",
			env:        map[string]string{settings.EnvAPIKey: "mk_from_env"},
			wantKey:    "mk_from_env",
			wantSource: settings.FromEnv,
			wantLabel:  "env MAILKUBE_API_KEY",
		},
		{
			name:       "the file wins over nothing",
			wantKey:    "mk_from_file",
			wantSource: settings.FromConfig,
			wantLabel:  "config file",
		},
		{
			// A variable present but empty is how a shell spells "not set". Treating it as
			// set would let a stray `export MAILKUBE_API_KEY=` blank a working config.
			name:       "an empty variable is not a value",
			env:        map[string]string{settings.EnvAPIKey: ""},
			wantKey:    "mk_from_file",
			wantSource: settings.FromConfig,
			wantLabel:  "config file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := settings.Resolve(tc.globals, settings.Overrides{}, stored(), output.MapEnv(tc.env))
			if got.APIKey.Value != tc.wantKey {
				t.Errorf("api key = %q, want %q", got.APIKey.Value, tc.wantKey)
			}
			if got.APIKey.Source != tc.wantSource {
				t.Errorf("source = %v, want %v", got.APIKey.Source, tc.wantSource)
			}
			if got.APIKey.Label() != tc.wantLabel {
				t.Errorf("label = %q, want %q", got.APIKey.Label(), tc.wantLabel)
			}
		})
	}
}

func TestTheProfileDecidesWhichCredentialsAreRead(t *testing.T) {
	t.Parallel()

	// The file selects "work"; the flag selects a profile that exists but holds nothing. A
	// resolver that read the active profile's values regardless would hand a command the wrong
	// credentials while reporting the right profile name.
	got := settings.Resolve(
		settings.Globals{Profile: "empty"}, settings.Overrides{}, stored(), output.MapEnv(nil))

	if got.Profile.Value != "empty" {
		t.Errorf("profile = %q, want %q", got.Profile.Value, "empty")
	}
	if got.APIKey.Set() {
		t.Errorf("read a credential from the wrong profile: %q", got.APIKey.Value)
	}
}

func TestAnUnnamedProfileFallsBackToTheFilesChoiceThenToTheDefault(t *testing.T) {
	t.Parallel()

	fromFile := settings.Resolve(settings.Globals{}, settings.Overrides{}, stored(), output.MapEnv(nil))
	if fromFile.Profile.Value != "work" {
		t.Errorf("profile = %q, want the file's active_profile %q", fromFile.Profile.Value, "work")
	}

	empty := settings.Resolve(settings.Globals{}, settings.Overrides{}, &configstore.Config{}, output.MapEnv(nil))
	if empty.Profile.Value != settings.DefaultProfile {
		t.Errorf("profile = %q, want %q", empty.Profile.Value, settings.DefaultProfile)
	}
	if empty.Profile.Label() != "(default)" {
		t.Errorf("label = %q, want %q", empty.Profile.Label(), "(default)")
	}
}

func TestTheBaseURLDefaultsToTheSDKsOwn(t *testing.T) {
	t.Parallel()

	// The CLI carries no URL literal of its own: it defers to the SDK's constant, which already
	// carries the trailing slash that reference resolution depends on.
	got := settings.Resolve(settings.Globals{}, settings.Overrides{}, &configstore.Config{}, output.MapEnv(nil))
	if got.BaseURL.Value != mailkube.DefaultBaseURL {
		t.Errorf("base url = %q, want the SDK's %q", got.BaseURL.Value, mailkube.DefaultBaseURL)
	}
}

func TestSMTPOverridesBeatTheStoredProfile(t *testing.T) {
	t.Parallel()

	got := settings.Resolve(settings.Globals{}, settings.Overrides{
		SMTPUser: "other@acme.com",
		SMTPPort: "465",
	}, stored(), output.MapEnv(nil))

	if got.SMTPUser.Value != "other@acme.com" || got.SMTPUser.Label() != "flag --smtp-user" {
		t.Errorf("smtp user = %q from %q", got.SMTPUser.Value, got.SMTPUser.Label())
	}
	if got.SMTPPort.Value != "465" {
		t.Errorf("smtp port = %q, want 465", got.SMTPPort.Value)
	}
	// The host was not overridden, so it still comes from the file: an override for one field
	// must not blank the others.
	if got.SMTPHost.Value != "smtp.example" {
		t.Errorf("smtp host = %q, want the stored one", got.SMTPHost.Value)
	}
}

func TestAnAbsentPortFallsBackToTheSubmissionDefault(t *testing.T) {
	t.Parallel()

	// A profile with a username and nothing else is the ordinary case: 587 with STARTTLS is
	// what a submission service is expected to offer, so requiring both to be written down
	// would be ceremony. The provenance still says where the value came from, so `config list`
	// shows "(default)" beside it and nothing about it is hidden.
	cfg := &configstore.Config{Profiles: map[string]configstore.Profile{
		"default": {SMTP: &configstore.SMTP{Username: "app01@acme.com"}},
	}}

	got := settings.Resolve(settings.Globals{}, settings.Overrides{}, cfg, output.MapEnv(nil))
	if got.SMTPPort.Value != settings.DefaultSMTPPort {
		t.Errorf("port = %q, want the default %q", got.SMTPPort.Value, settings.DefaultSMTPPort)
	}
	if got.SMTPPort.Source != settings.FromDefault {
		t.Errorf("port source = %v, want it attributed to the default", got.SMTPPort.Source)
	}
	if got.SMTPTLS.Value != settings.DefaultSMTPTLS {
		t.Errorf("tls = %q, want the default %q", got.SMTPTLS.Value, settings.DefaultSMTPTLS)
	}

	// A zero in the file is still not a port: the config store writes the field only when it
	// has been set, and a stored 0 would resolve to a dial that cannot work.
	zero := &configstore.Config{Profiles: map[string]configstore.Profile{
		"default": {SMTP: &configstore.SMTP{Username: "app01@acme.com", Port: configstore.Port(0)}},
	}}
	if got := settings.Resolve(settings.Globals{}, settings.Overrides{}, zero, output.MapEnv(nil)); got.SMTPPort.Value == "0" {
		t.Error("a stored zero was resolved as a port")
	}
}

func TestTheTimeoutComesFromTheFlagOrTheDefault(t *testing.T) {
	t.Parallel()

	def := settings.Resolve(settings.Globals{}, settings.Overrides{}, nil, output.MapEnv(nil))
	if def.Timeout.Value != settings.DefaultTimeout.String() || def.Timeout.Source != settings.FromDefault {
		t.Errorf("timeout = %q from %v", def.Timeout.Value, def.Timeout.Source)
	}

	set := settings.Resolve(
		settings.Globals{Timeout: 5 * time.Second}, settings.Overrides{}, nil, output.MapEnv(nil))
	if set.Timeout.Value != "5s" || set.Timeout.Label() != "flag --timeout" {
		t.Errorf("timeout = %q from %q", set.Timeout.Value, set.Timeout.Label())
	}
}

func TestAnUnsetFormatIsAnAbsenceRatherThanADefault(t *testing.T) {
	t.Parallel()

	// Nothing "defaults" the output format: with no flag it is inferred from whether the
	// stream is a terminal, which is not one of the four sources at all.
	got := settings.Resolve(settings.Globals{}, settings.Overrides{}, nil, output.MapEnv(nil))
	if got.Output.Set() {
		t.Errorf("output = %q, want it reported as unset", got.Output.Value)
	}
}

func TestResolveToleratesNoConfigFileAtAll(t *testing.T) {
	t.Parallel()

	// A first run has no file, and every command still has to resolve its settings rather than
	// fail on the absence.
	got := settings.Resolve(settings.Globals{}, settings.Overrides{}, nil, output.MapEnv(nil))
	if got.Profile.Value != settings.DefaultProfile {
		t.Errorf("profile = %q, want %q", got.Profile.Value, settings.DefaultProfile)
	}
	if got.APIKey.Set() {
		t.Errorf("api key = %q, want none", got.APIKey.Value)
	}
}
