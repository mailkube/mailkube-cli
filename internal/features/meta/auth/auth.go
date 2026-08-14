// Package auth implements `mailkube auth`: storing and reporting credentials.
//
// Two credentials live side by side and neither substitutes for the other. The API key
// authenticates the REST transport; the SMTP pair authenticates submission for one sending
// domain and is issued separately. Every verb here says which one it is acting on, and nothing
// ever falls back from one to the other: a missing SMTP credential is an error to report, not a
// reason to quietly use the API instead.
package auth

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// SenderFor builds the client the credential probe runs against.
//
// It is a field on the feature rather than a call inside it, so a test can substitute a fake
// server without a network. This is the same seam the client factory exposes for TLS, and it is
// the only one: there is no flag anywhere that weakens verification.
type SenderFor func(deps *feature.Deps, r settings.Resolved) (ports.EmailSender, error)

// Feature stores and reports credentials.
type Feature struct {
	// Sender builds the probe's client. Nil means the real one.
	Sender SenderFor
}

// New returns the auth feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "auth" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "auth login",
		Summary:    "Store credentials",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Store and inspect credentials",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.loginCmd(deps), f.statusCmd(deps), f.logoutCmd(deps))
	return cmd
}

// loginCmd stores a credential, verifying it first unless told not to.
func (f *Feature) loginCmd(deps *feature.Deps) *cobra.Command {
	var (
		key      string
		user     string
		useSMTP  bool
		noVerify bool
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an API key, or SMTP credentials with --smtp",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if useSMTP {
				view, err := f.LoginSMTP(deps, user)
				if err != nil {
					return err
				}
				return deps.Emit(view)
			}
			view, err := f.LoginAPI(c.Context(), deps, key, noVerify)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "the API key to store; prompted for when omitted")
	cmd.Flags().BoolVar(&useSMTP, "smtp", false, "store SMTP credentials instead of an API key")
	cmd.Flags().StringVar(&user, "user", "", "the SMTP username, localpart@verified-domain")
	// Provisioning a config on a machine with no network is a real workflow, and the honest
	// answer there is to store the credential and say plainly that it was not checked.
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "store the credential without checking it")
	return cmd
}

// LoginAPI stores an API key.
//
// The key is never accepted as a positional argument and, when not passed with --key, is read
// without echo: both of those keep it out of shell history and out of the process list.
func (f *Feature) LoginAPI(ctx context.Context, deps *feature.Deps, key string, noVerify bool) (LoginView, error) {
	key, err := f.resolveKey(deps, key)
	if err != nil {
		return LoginView{}, err
	}

	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return LoginView{}, err
	}

	view := LoginView{Principal: "api", Profile: r.Profile.Value, Path: deps.Store.Path()}
	if !noVerify {
		view.Verification, err = f.probe(ctx, deps, r, key)
		if err != nil {
			return LoginView{}, err
		}
	}

	if err := storeAPIKey(deps, r.Profile.Value, key); err != nil {
		return LoginView{}, err
	}
	view.Stored = true
	view.Masked = maskedKey(deps, key)
	return view, nil
}

// resolveKey takes the key from the flag, the environment, or a prompt, in that order.
func (f *Feature) resolveKey(deps *feature.Deps, key string) (string, error) {
	if key != "" {
		return key, nil
	}
	if v, ok := deps.Env(settings.EnvAPIKey); ok && v != "" {
		return v, nil
	}

	typed, err := deps.Prompter().Secret(
		"Paste your Mailkube API key (input hidden):",
		errs.Usagef("no API key given, and there is no terminal to ask on\nPass --key, or set %s.",
			settings.EnvAPIKey))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(typed) == "" {
		return "", errs.Validationf("no API key given")
	}
	return typed, nil
}

// probe runs the credential check against a client built with the key being stored.
//
// The key under test is substituted into the resolved settings rather than read back from the
// config, because it has not been stored yet — and storing first would leave a rejected key in
// the file if the check failed.
func (f *Feature) probe(ctx context.Context, deps *feature.Deps, r settings.Resolved, key string) (Verification, error) {
	r.APIKey = settings.Value{Value: key, Source: settings.FromFlag, Origin: "--key"}

	sender, err := f.sender(deps, r)
	if err != nil {
		return Verification{}, err
	}
	return verifyKey(ctx, sender)
}

// sender returns the configured seam, or the real client.
func (f *Feature) sender(deps *feature.Deps, r settings.Resolved) (ports.EmailSender, error) {
	if f.Sender != nil {
		return f.Sender(deps, r)
	}
	client, err := deps.Factory(r).Client()
	if err != nil {
		return nil, err
	}
	return client.Emails, nil
}

// storeAPIKey writes the key into one profile, creating the profile if it is new.
func storeAPIKey(deps *feature.Deps, profile, key string) error {
	err := deps.Store.Update(func(cfg *configstore.Config) error {
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]configstore.Profile{}
		}
		p := cfg.Profiles[profile]
		p.APIKey = key
		cfg.Profiles[profile] = p
		// A first credential also settles which profile is active. Without this a user who
		// logged into a named profile would store a key and then have every command ignore it.
		if cfg.ActiveProfile == "" {
			cfg.ActiveProfile = profile
		}
		return nil
	})
	if err != nil {
		return err
	}
	deps.ForgetConfig()
	return nil
}

// LoginSMTP stores the SMTP credential for one sending domain.
//
// The username is shape-checked before anything else happens, and nothing here opens a socket:
// the AUTH check belongs with the SMTP transport, and a login command that silently skipped a
// verification it claimed to perform would be worse than one that does not claim it.
func (f *Feature) LoginSMTP(deps *feature.Deps, user string) (LoginView, error) {
	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return LoginView{}, err
	}

	user, err = f.resolveSMTPUser(deps, user, r)
	if err != nil {
		return LoginView{}, err
	}
	if err := input.ValidateSMTPUsername(user); err != nil {
		return LoginView{}, err
	}

	password, err := f.resolveSMTPPassword(deps, r)
	if err != nil {
		return LoginView{}, err
	}

	err = deps.Store.Update(func(cfg *configstore.Config) error {
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]configstore.Profile{}
		}
		p := cfg.Profiles[r.Profile.Value]
		if p.SMTP == nil {
			p.SMTP = &configstore.SMTP{}
		}
		p.SMTP.Username = user
		p.SMTP.Password = password
		cfg.Profiles[r.Profile.Value] = p
		if cfg.ActiveProfile == "" {
			cfg.ActiveProfile = r.Profile.Value
		}
		return nil
	})
	if err != nil {
		return LoginView{}, err
	}

	deps.ForgetConfig()
	return LoginView{
		Principal: "smtp",
		Profile:   r.Profile.Value,
		Path:      deps.Store.Path(),
		Stored:    true,
		Masked:    user,
	}, nil
}

// resolveSMTPUser takes the username from the flag, the stored profile, or a prompt.
func (f *Feature) resolveSMTPUser(deps *feature.Deps, user string, r settings.Resolved) (string, error) {
	if user != "" {
		return user, nil
	}
	if r.SMTPUser.Set() {
		return r.SMTPUser.Value, nil
	}
	return deps.Prompter().Line("SMTP username:",
		errs.Usagef("no SMTP username given, and there is no terminal to ask on\nPass --user."))
}

// resolveSMTPPassword takes the password from the environment or a prompt, never from a flag.
//
// A flag would put the credential in shell history and in the process list, which is precisely
// the exposure it exists to prevent, so there is no flag to pass it with at all.
func (f *Feature) resolveSMTPPassword(deps *feature.Deps, r settings.Resolved) (string, error) {
	if r.SMTPPassword.Set() {
		return r.SMTPPassword.Value, nil
	}
	password, err := deps.Prompter().Secret(
		"Password (input hidden):",
		errs.Usagef("no SMTP password given, and there is no terminal to ask on\nSet %s.",
			settings.EnvSMTPPassword))
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errs.Validationf("no SMTP password given")
	}
	return password, nil
}

// statusCmd reports what is configured.
func (f *Feature) statusCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which credentials are configured, without contacting anything",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			view, err := f.status(deps)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// status describes both principals from local state alone.
//
// It contacts nothing. Credentials are verified when they are stored; re-probing here would
// spend a rate-limit slot every time someone asked a question about their own configuration.
//
// It also does not report the domain the key is bound to. That value exists only inside the
// rejection message of the login probe, and a cached copy would go stale silently.
func (f *Feature) status(deps *feature.Deps) (StatusView, error) {
	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return StatusView{}, err
	}

	view := StatusView{Profile: r.Profile.Value}
	if r.APIKey.Set() {
		view.APIKey = maskedKey(deps, r.APIKey.Value)
		view.APIKeySource = r.APIKey.Label()
	}
	if r.SMTPUser.Set() {
		view.SMTPUser = r.SMTPUser.Value
		view.SMTPSource = r.SMTPUser.Label()
		view.SMTPPasswordSet = r.SMTPPassword.Set()
	}
	return view, nil
}

// logoutCmd removes a credential.
func (f *Feature) logoutCmd(deps *feature.Deps) *cobra.Command {
	var useSMTP bool

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored API key, or the SMTP credential with --smtp",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			view, err := f.logout(deps, useSMTP)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
	cmd.Flags().BoolVar(&useSMTP, "smtp", false, "remove the SMTP credential instead of the API key")
	return cmd
}

// logout removes one credential, leaving the profile and the other credential in place.
//
// Removing the profile would be a larger action than the verb names, and the two credentials are
// independent principals: signing out of REST has no bearing on submission.
func (f *Feature) logout(deps *feature.Deps, useSMTP bool) (LogoutView, error) {
	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return LogoutView{}, err
	}
	profile := r.Profile.Value

	err = deps.Store.Update(func(cfg *configstore.Config) error {
		p, ok := cfg.Profiles[profile]
		if !ok {
			return errs.Newf(errs.CodeNotFound, "no profile named %q", profile)
		}
		if useSMTP {
			p.SMTP = nil
		} else {
			p.APIKey = ""
		}
		cfg.Profiles[profile] = p
		return nil
	})
	if err != nil {
		return LogoutView{}, err
	}

	deps.ForgetConfig()
	return LogoutView{Principal: principal(useSMTP), Profile: profile}, nil
}

// principal names which credential a verb acted on.
func principal(useSMTP bool) string {
	if useSMTP {
		return "smtp"
	}
	return "api"
}

// maskedKey renders a key so it can be recognised but not used.
func maskedKey(deps *feature.Deps, key string) string {
	return maskWith(deps.Caps.Glyphs.Ellipsis, key)
}
