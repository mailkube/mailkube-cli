// Package settings resolves one effective value per setting, and remembers where it came from.
//
// Four sources feed every setting — a flag, an environment variable, the config file, and a
// built-in default — in that order of precedence. Resolving them in one place is what keeps the
// order a single rule rather than a per-command habit.
//
// Provenance is carried alongside the value rather than discarded, because "why is it using that
// base URL?" is a question a user should be able to answer from their own terminal. A resolver
// that returned only the winning string would make that a support conversation.
package settings

import (
	"strconv"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// DefaultProfile is the profile used when none is named.
const DefaultProfile = "default"

// DefaultTimeout bounds a single API call.
const DefaultTimeout = 30 * time.Second

// The submission defaults, which exist so that configuring a username and a host is enough.
//
// 587 with STARTTLS is what a submission service is expected to offer, so requiring both to be
// stated would be ceremony for the common case — and the values are shown by `config list` with
// "(default)" beside them, so nothing about them is hidden.
const (
	// DefaultSMTPPort is the submission port.
	DefaultSMTPPort = "587"
	// DefaultSMTPTLS is the encryption mode. There is no unencrypted option anywhere.
	DefaultSMTPTLS = "starttls"
)

// The environment variables this package reads. They are named here rather than at each use so
// the set is enumerable: `doctor` and `config list` both report on them, and a variable read in
// one place and unknown to the other is a setting the user cannot diagnose.
const (
	EnvProfile      = "MAILKUBE_PROFILE"
	EnvAPIKey       = "MAILKUBE_API_KEY"
	EnvBaseURL      = "MAILKUBE_BASE_URL"
	EnvConfig       = "MAILKUBE_CONFIG"
	EnvSMTPPassword = "MAILKUBE_SMTP_PASSWORD"
	EnvSkillDir     = "MAILKUBE_SKILL_DIR"
	// EnvWebhookSecret is the signing secret the local listener verifies deliveries against.
	//
	// It is read from the environment and never written to the config file. A webhook secret
	// belongs to one endpoint rather than to a profile, and a developer typically holds
	// several at once; storing one in a profile would make the wrong one the default.
	EnvWebhookSecret = "MAILKUBE_WEBHOOK_SECRET"
)

// Source is where a resolved value came from.
type Source int

// The sources, in increasing order of precedence.
const (
	// FromDefault means nothing configured it and the built-in value applies.
	FromDefault Source = iota
	// FromConfig means it came from the config file.
	FromConfig
	// FromEnv means an environment variable set it.
	FromEnv
	// FromFlag means it was given on the command line.
	FromFlag
)

// Value is one resolved setting together with its provenance.
type Value struct {
	// Value is the effective value, empty when nothing supplied one.
	Value string
	// Source is which of the four inputs won.
	Source Source
	// Origin names the specific input — the variable or the flag — when there is one.
	Origin string
}

// Label renders the provenance the way `config list` shows it.
//
// The default case is parenthesised and the others are not, so a value nobody chose is visibly
// different from one somebody did: those are the rows a user scans for when the CLI is not doing
// what they expected.
func (v Value) Label() string {
	switch v.Source {
	case FromFlag:
		return "flag " + v.Origin
	case FromEnv:
		return "env " + v.Origin
	case FromConfig:
		return "config file"
	case FromDefault:
		return "(default)"
	default:
		return "(default)"
	}
}

// Set reports whether anything actually supplied this value.
func (v Value) Set() bool { return v.Value != "" }

// Resolved is the full set of settings, each with its provenance.
type Resolved struct {
	// Profile is the profile whose values were read.
	Profile Value
	// APIKey authenticates the REST transport.
	APIKey Value
	// BaseURL is the API root; its trailing slash is load-bearing.
	BaseURL Value
	// Timeout bounds a single call.
	Timeout Value
	// Output is the forced output format, empty when it is inferred from the terminal.
	Output Value
	// SMTPUser is the SMTP principal, which is a different credential from APIKey.
	SMTPUser Value
	// SMTPPassword is the SMTP secret. It is resolved so a command can use it and is never
	// rendered by any screen.
	SMTPPassword Value
	// SMTPHost is the submission host.
	SMTPHost Value
	// SMTPPort is the submission port, as text so it renders like every other row.
	SMTPPort Value
	// SMTPTLS is the transport security mode.
	SMTPTLS Value
}

// Overrides are the per-command flags that beat the config file for one invocation.
//
// They are separate from Globals because they are not global: only the commands that speak SMTP
// define them. Threading them through Resolve rather than resolving them inside those commands
// keeps one precedence rule in the program instead of two.
type Overrides struct {
	// SMTPUser is the submission principal, localpart@verified-domain.
	SMTPUser string
	// SMTPHost is the submission host.
	SMTPHost string
	// SMTPPort is the submission port.
	SMTPPort string
	// SMTPTLS is the transport security mode.
	SMTPTLS string
}

// Resolve applies the precedence rule to every setting.
//
// The config argument is the whole file rather than one profile, because which profile to read is
// itself one of the settings being resolved.
func Resolve(g Globals, o Overrides, cfg *configstore.Config, env output.Lookup) Resolved {
	profile := pick(input{
		flag: g.Profile, flagName: "--profile",
		envKey: EnvProfile, env: env,
		config: activeProfile(cfg),
		def:    DefaultProfile,
	})

	p := profileNamed(cfg, profile.Value)
	smtp := p.SMTP
	if smtp == nil {
		smtp = &configstore.SMTP{}
	}

	return Resolved{
		Profile: profile,
		APIKey: pick(input{
			flag: g.APIKey, flagName: "--api-key",
			envKey: EnvAPIKey, env: env,
			config: p.APIKey,
		}),
		BaseURL: pick(input{
			flag: g.BaseURL, flagName: "--base-url",
			envKey: EnvBaseURL, env: env,
			config: p.BaseURL,
			def:    mailkube.DefaultBaseURL,
		}),
		Timeout:      timeout(g),
		Output:       forcedFormat(g),
		SMTPUser:     pick(input{flag: o.SMTPUser, flagName: "--smtp-user", config: smtp.Username}),
		SMTPPassword: pick(input{envKey: EnvSMTPPassword, env: env, config: smtp.Password}),
		SMTPHost:     pick(input{flag: o.SMTPHost, flagName: "--smtp-host", config: smtp.Host}),
		SMTPPort: pick(input{
			flag: o.SMTPPort, flagName: "--smtp-port", config: port(smtp.Port), def: DefaultSMTPPort,
		}),
		SMTPTLS: pick(input{
			flag: o.SMTPTLS, flagName: "--smtp-tls", config: smtp.TLS, def: string(DefaultSMTPTLS),
		}),
	}
}

// timeout is resolved separately because its default is a duration rather than a string, and
// because it has no environment variable: a timeout is a property of one invocation.
func timeout(g Globals) Value {
	if g.Timeout > 0 {
		return Value{Value: g.Timeout.String(), Source: FromFlag, Origin: "--timeout"}
	}
	return Value{Value: DefaultTimeout.String(), Source: FromDefault}
}

// forcedFormat reports the format the user demanded. An unset value is not a default but an
// absence: the format is then inferred from whether the stream is a terminal, which is not a
// source at all.
func forcedFormat(g Globals) Value {
	if g.Output != "" {
		return Value{Value: g.Output, Source: FromFlag, Origin: "--output"}
	}
	return Value{Value: "", Source: FromDefault}
}

// input is one setting's four candidate sources, gathered so pick reads as the precedence rule
// itself rather than as four nested conditions repeated per setting.
type input struct {
	flag     string
	flagName string
	envKey   string
	env      output.Lookup
	config   string
	def      string
}

// pick applies flag > env > config > default.
func pick(in input) Value {
	if in.flag != "" {
		return Value{Value: in.flag, Source: FromFlag, Origin: in.flagName}
	}
	if in.envKey != "" && in.env != nil {
		// A variable set to the empty string is how a shell spells "not set". Treating it as
		// set would let a stray `export MAILKUBE_PROFILE=` blank a working config.
		if v, ok := in.env(in.envKey); ok && v != "" {
			return Value{Value: v, Source: FromEnv, Origin: in.envKey}
		}
	}
	if in.config != "" {
		return Value{Value: in.config, Source: FromConfig}
	}
	return Value{Value: in.def, Source: FromDefault}
}

// activeProfile is the profile the config file itself selects, if any.
func activeProfile(cfg *configstore.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.ActiveProfile
}

// profileNamed returns a profile by name, or an empty one. A missing profile is not an error
// here: naming a profile that does not exist yet is exactly what `auth login --profile new` does.
func profileNamed(cfg *configstore.Config, name string) configstore.Profile {
	if cfg == nil {
		return configstore.Profile{}
	}
	return cfg.Profiles[name]
}

// port renders a stored port number, treating zero as unset rather than as the number 0.
func port(p *int) string {
	// Both an absent pointer and a stored zero mean "not configured". The pointer is what keeps
	// an unset port out of the file in the first place, but a file can be edited by hand, and a
	// zero that resolved as a port would produce a dial to nowhere with nothing to explain it.
	if p == nil || *p <= 0 {
		return ""
	}
	return strconv.Itoa(*p)
}
