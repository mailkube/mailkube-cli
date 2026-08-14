package config

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// tlsModes are the accepted values for the smtp_tls key.
func tlsModes() []string { return []string{"starttls", "implicit"} }

// key is one settable field of a profile.
//
// Reading and writing are a pair of functions rather than reflection over the config struct: the
// set of keys a user may edit is a smaller and more stable thing than the set of fields the file
// happens to have, and stating it explicitly is what lets a field exist in the file without
// becoming a supported command-line key by accident.
type key struct {
	// name is the key as the user types it.
	name string
	// write stores a value, validating it first.
	//
	// There is deliberately no read half. Reading goes through the settings resolver, which
	// is the only thing that can report which of the four sources a value came from, and a
	// second reader here would be a second answer to what a key currently is.
	write func(*configstore.Profile, string) error
}

// keys is every settable key, in the order `config list` shows them.
//
// A function rather than a package-level slice, because package-level mutable state is banned
// repository-wide, and this is read often enough that the allocation is irrelevant.
func keys() []key {
	return []key{
		{
			name: "api_key",
			write: func(p *configstore.Profile, v string) error {
				p.APIKey = v
				return nil
			},
		},
		{
			name: "base_url",
			write: func(p *configstore.Profile, v string) error {
				p.BaseURL = v
				return nil
			},
		},
		{
			name: "smtp_user",
			write: func(p *configstore.Profile, v string) error {
				if err := input.ValidateSMTPUsername(v); err != nil {
					return err
				}
				ensureSMTP(p).Username = v
				return nil
			},
		},
		{
			name: "smtp_host",
			write: func(p *configstore.Profile, v string) error {
				ensureSMTP(p).Host = v
				return nil
			},
		},
		{
			name:  "smtp_port",
			write: writePort,
		},
		{
			name:  "smtp_tls",
			write: writeTLS,
		},
	}
}

// writePort stores a port, rejecting anything that is not one.
func writePort(p *configstore.Profile, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return errs.Validationf("invalid smtp_port %q\nA port is a number between 1 and 65535.", v)
	}
	ensureSMTP(p).Port = configstore.Port(n)
	return nil
}

// writeTLS stores a transport security mode from the closed set of modes that exist.
func writeTLS(p *configstore.Profile, v string) error {
	for _, mode := range tlsModes() {
		if v == mode {
			ensureSMTP(p).TLS = v
			return nil
		}
	}
	return errs.Validationf("invalid smtp_tls %q\nUse one of: %s.", v, strings.Join(tlsModes(), ", "))
}

// lookupKey finds a key by name, reporting the alternatives when there is no such key.
//
// The message lists them rather than saying "unknown key", because a user who mistyped one is
// one line away from the answer and a user who invented one needs to see that they did.
func lookupKey(name string) (key, error) {
	for _, k := range keys() {
		if k.name == name {
			return k, nil
		}
	}

	names := make([]string, 0, len(keys()))
	for _, k := range keys() {
		names = append(names, k.name)
	}
	sort.Strings(names)
	return key{}, errs.Usagef("unknown setting %q\nSettable keys: %s.", name, strings.Join(names, ", "))
}

// smtpOf reads a profile's SMTP block, treating an absent one as empty.
func smtpOf(p configstore.Profile) configstore.SMTP {
	if p.SMTP == nil {
		return configstore.SMTP{}
	}
	return *p.SMTP
}

// ensureSMTP returns the profile's SMTP block, creating it on first write.
func ensureSMTP(p *configstore.Profile) *configstore.SMTP {
	if p.SMTP == nil {
		p.SMTP = &configstore.SMTP{}
	}
	return p.SMTP
}

// secretKeys are the keys whose values are never printed in full.
func secretKeys() map[string]bool { return map[string]bool{"api_key": true} }

// rejectPassword refuses to take an SMTP password as an argument.
//
// An argument lands in shell history and in the process list, which is exactly the exposure the
// credential exists to avoid. The prompt and the environment variable are the supported paths.
func rejectPassword(name string) error {
	if name != "smtp_password" {
		return nil
	}
	return errs.Usagef(
		"smtp_password cannot be set as an argument, because an argument is visible in your\n" +
			"shell history and in the process list.\n" +
			"Set it with `mailkube auth login --smtp`, or through " + settings.EnvSMTPPassword + ".")
}
