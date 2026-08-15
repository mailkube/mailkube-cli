// Package config implements `mailkube config`: reading, writing and explaining settings.
//
// Its distinguishing job is provenance. Four inputs feed every setting, and a command that
// reported only the winning value would leave "why is it using that base URL?" answerable only
// by someone with access to the machine. Every read verb here reports where the value came from.
package config

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// Feature manages profiles and settings.
type Feature struct{}

// New returns the config feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "config" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "config",
		Summary:    "Manage profiles and settings",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage profiles and settings",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(
		f.listCmd(deps),
		f.getCmd(deps),
		f.setCmd(deps),
		f.unsetCmd(deps),
		f.pathCmd(deps),
		f.profileCmd(deps),
	)
	return cmd
}

// listCmd shows every setting with its effective value and its source.
func (f *Feature) listCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show every setting, its value and where it came from",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			view, err := f.list(deps)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// list builds the settings table.
//
// The SMTP rows appear only when something configured them, because the common case is a user
// who never touches SMTP, and three permanently empty rows in the middle of the table would
// train them to stop reading it.
func (f *Feature) list(deps *feature.Deps) (ListView, error) {
	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return ListView{}, err
	}

	view := ListView{Settings: []SettingView{
		row("profile", r.Profile, deps.Caps),
		row("api_key", r.APIKey, deps.Caps),
		row("base_url", r.BaseURL, deps.Caps),
		row("smtp_user", r.SMTPUser, deps.Caps),
	}}
	for _, optional := range []struct {
		name  string
		value settings.Value
	}{
		{"smtp_host", r.SMTPHost},
		{"smtp_port", r.SMTPPort},
		{"smtp_tls", r.SMTPTLS},
	} {
		if optional.value.Set() {
			view.Settings = append(view.Settings, row(optional.name, optional.value, deps.Caps))
		}
	}
	view.Settings = append(view.Settings,
		row("timeout", r.Timeout, deps.Caps),
		row("output", r.Output, deps.Caps),
	)
	return view, nil
}

// row renders one resolved setting, masking it when it is a credential.
func row(name string, v settings.Value, caps output.Caps) SettingView {
	if !v.Set() {
		return SettingView{Setting: name, Value: notSet, Source: "(not set)"}
	}
	value := v.Value
	if secretKeys()[name] {
		value = output.Secret(value, caps.Glyphs.Ellipsis)
	}
	return SettingView{Setting: name, Value: value, Source: v.Label()}
}

// getCmd prints one setting's value.
func (f *Feature) getCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "get <setting>",
		Short: "Print one setting's value",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			view, err := f.get(deps, args[0])
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// get resolves one setting by name.
func (f *Feature) get(deps *feature.Deps, name string) (GetView, error) {
	if err := rejectPassword(name); err != nil {
		return GetView{}, err
	}
	if _, err := lookupKey(name); err != nil {
		return GetView{}, err
	}

	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return GetView{}, err
	}

	value, ok := resolvedByName(r, name)
	if !ok {
		return GetView{}, errs.Newf(errs.CodeInternal, "setting %q has no resolved value", name)
	}
	rendered := row(name, value, deps.Caps)
	return GetView{Setting: name, Value: rendered.Value, Source: rendered.Source}, nil
}

// resolvedByName maps a key name onto the resolved value it corresponds to.
//
// The mapping is explicit rather than reflective so that adding a key is a compile-time question:
// a key with no resolved value fails here, visibly, instead of quietly reading as unset.
func resolvedByName(r settings.Resolved, name string) (settings.Value, bool) {
	switch name {
	case "api_key":
		return r.APIKey, true
	case "base_url":
		return r.BaseURL, true
	case "smtp_user":
		return r.SMTPUser, true
	case "smtp_host":
		return r.SMTPHost, true
	case "smtp_port":
		return r.SMTPPort, true
	case "smtp_tls":
		return r.SMTPTLS, true
	default:
		return settings.Value{}, false
	}
}

// setCmd stores a value in the config file.
func (f *Feature) setCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "set <setting> <value>",
		Short: "Store a setting in the config file",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			view, err := f.set(deps, args[0], args[1])
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// set writes one key into the active profile.
func (f *Feature) set(deps *feature.Deps, name, value string) (WriteView, error) {
	if err := rejectPassword(name); err != nil {
		return WriteView{}, err
	}
	k, err := lookupKey(name)
	if err != nil {
		return WriteView{}, err
	}

	profile, err := activeProfileName(deps)
	if err != nil {
		return WriteView{}, err
	}

	err = deps.Store.Update(func(cfg *configstore.Config) error {
		p := cfg.Profiles[profile]
		if writeErr := k.write(&p, value); writeErr != nil {
			return writeErr
		}
		ensureProfiles(cfg)[profile] = p
		return nil
	})
	if err != nil {
		return WriteView{}, err
	}

	deps.ForgetConfig()
	return WriteView{Setting: name, Profile: profile, Action: "set", Path: deps.Store.Path()}, nil
}

// unsetCmd removes a value from the config file.
func (f *Feature) unsetCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "unset <setting>",
		Short: "Remove a setting from the config file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			view, err := f.unset(deps, args[0])
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// unset clears one key in the active profile.
//
// Clearing rather than validating: every key's zero value is a legal absence, so this bypasses
// the write validators, which exist to reject a bad value rather than to reject no value.
func (f *Feature) unset(deps *feature.Deps, name string) (WriteView, error) {
	if err := rejectPassword(name); err != nil {
		return WriteView{}, err
	}
	if _, err := lookupKey(name); err != nil {
		return WriteView{}, err
	}

	profile, err := activeProfileName(deps)
	if err != nil {
		return WriteView{}, err
	}

	err = deps.Store.Update(func(cfg *configstore.Config) error {
		p := cfg.Profiles[profile]
		clear(&p, name)
		ensureProfiles(cfg)[profile] = p
		return nil
	})
	if err != nil {
		return WriteView{}, err
	}

	deps.ForgetConfig()
	return WriteView{Setting: name, Profile: profile, Action: "unset", Path: deps.Store.Path()}, nil
}

// clear removes one key's value from a profile.
func clear(p *configstore.Profile, name string) {
	switch name {
	case "api_key":
		p.APIKey = ""
	case "base_url":
		p.BaseURL = ""
	case "smtp_user":
		ensureSMTP(p).Username = ""
	case "smtp_host":
		ensureSMTP(p).Host = ""
	case "smtp_port":
		ensureSMTP(p).Port = nil
	case "smtp_tls":
		ensureSMTP(p).TLS = ""
	}
}

// pathCmd reports where the config file lives.
func (f *Feature) pathCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the path to the config file",
		Args:  cobra.NoArgs,
		// This never reads the file. It is one of the two commands that must still work
		// when the file cannot be parsed, because it is how a user finds the file they have
		// been told to repair.
		RunE: func(_ *cobra.Command, _ []string) error {
			return deps.Emit(PathView{Path: deps.Store.Path()})
		},
	}
}

// profileCmd groups the profile verbs.
func (f *Feature) profileCmd(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "List, switch and delete profiles",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.profileListCmd(deps), f.profileUseCmd(deps), f.profileDeleteCmd(deps))
	return cmd
}

// profileListCmd shows the stored profiles.
func (f *Feature) profileListCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stored profiles",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			view, err := f.profileList(deps)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// profileList describes each profile by which credentials it holds.
func (f *Feature) profileList(deps *feature.Deps) (ProfileListView, error) {
	cfg, err := deps.Config()
	if err != nil {
		return ProfileListView{}, err
	}
	active, err := activeProfileName(deps)
	if err != nil {
		return ProfileListView{}, err
	}

	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	view := ProfileListView{}
	for _, name := range names {
		p := cfg.Profiles[name]
		view.Profiles = append(view.Profiles, ProfileView{
			Name:      name,
			Active:    name == active,
			HasAPIKey: p.APIKey != "",
			HasSMTP:   smtpOf(p).Username != "",
		})
	}
	return view, nil
}

// profileUseCmd switches the active profile.
func (f *Feature) profileUseCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "use <profile>",
		Short: "Make a profile the one commands use by default",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			view, err := f.profileUse(deps, args[0])
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// profileUse is the only writer of the active profile.
//
// It refuses to select a profile that does not exist. Creating one as a side effect of selecting
// it would leave a user pointed at an empty profile and wondering why their credentials vanished.
func (f *Feature) profileUse(deps *feature.Deps, name string) (ProfileUseView, error) {
	err := deps.Store.Update(func(cfg *configstore.Config) error {
		if _, ok := cfg.Profiles[name]; !ok {
			return errs.Configf(
				"no profile named %q\nCreate it with `mailkube auth login --profile %s`.", name, name)
		}
		cfg.ActiveProfile = name
		return nil
	})
	if err != nil {
		return ProfileUseView{}, err
	}

	deps.ForgetConfig()
	return ProfileUseView{Profile: name}, nil
}

// profileDeleteCmd removes a profile.
func (f *Feature) profileDeleteCmd(deps *feature.Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <profile>",
		Short: "Delete a profile and the credentials in it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			view, err := f.profileDelete(deps, args[0])
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
}

// profileDelete removes a profile after confirming, because it destroys credentials.
func (f *Feature) profileDelete(deps *feature.Deps, name string) (ProfileDeleteView, error) {
	cfg, err := deps.Config()
	if err != nil {
		return ProfileDeleteView{}, err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return ProfileDeleteView{}, errs.Newf(errs.CodeNotFound, "no profile named %q", name)
	}

	ok, err := deps.Confirm("Delete profile " + name + " and the credentials in it?")
	if err != nil {
		return ProfileDeleteView{}, err
	}
	if !ok {
		return ProfileDeleteView{}, errs.Newf(errs.CodeConfig, "canceled; nothing was deleted")
	}

	err = deps.Store.Update(func(c *configstore.Config) error {
		delete(c.Profiles, name)
		if c.ActiveProfile == name {
			c.ActiveProfile = ""
		}
		return nil
	})
	if err != nil {
		return ProfileDeleteView{}, err
	}

	deps.ForgetConfig()
	return ProfileDeleteView{Profile: name}, nil
}

// activeProfileName is the profile every write verb targets.
func activeProfileName(deps *feature.Deps) (string, error) {
	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return "", err
	}
	return r.Profile.Value, nil
}

// ensureProfiles returns the profile map, creating it for a config file that has none yet.
func ensureProfiles(cfg *configstore.Config) map[string]configstore.Profile {
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]configstore.Profile{}
	}
	return cfg.Profiles
}
