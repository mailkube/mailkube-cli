package config

import (
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// notSet is the value cell for a setting nothing supplied.
//
// A dash rather than an empty cell, because an empty cell in an aligned table is
// indistinguishable from a rendering fault, and the reader's question here is precisely whether
// the value is missing or merely invisible.
const notSet = "-"

// SettingView is one row of `config list`.
type SettingView struct {
	// Setting is the key name.
	Setting string `json:"setting"`
	// Value is the effective value, masked when the key holds a credential.
	Value string `json:"value"`
	// Source is where it came from, in the same words the human table shows.
	Source string `json:"source"`
}

// ListView is the whole of `config list`.
//
// It answers "why is it using that value", which is the question that otherwise becomes a
// support conversation: the effective value alone tells a user what is happening but not which
// of four inputs to go and change.
type ListView struct {
	// Settings are the rows, in a fixed order rather than an alphabetical one, so the
	// credentials sit together and the presentation settings sit at the end.
	Settings []SettingView `json:"settings"`
}

// RenderText implements output.TextRenderer.
func (v ListView) RenderText(_ output.Caps) []string {
	table := output.Table{Headers: []string{"SETTING", "VALUE", "SOURCE"}}
	for _, s := range v.Settings {
		table.Rows = append(table.Rows, []string{s.Setting, s.Value, s.Source})
	}
	return table.Lines()
}

// GetView is one setting, as `config get` reports it.
type GetView struct {
	// Setting is the key name.
	Setting string `json:"setting"`
	// Value is the effective value, masked when the key holds a credential.
	Value string `json:"value"`
	// Source is where it came from.
	Source string `json:"source"`
}

// RenderText implements output.TextRenderer.
//
// The bare value, with no label and no provenance: `config get` is what a script reads, so its
// text form is the value and nothing else. The provenance is still in the JSON form, where a
// caller that wants it can ask.
func (v GetView) RenderText(_ output.Caps) []string { return []string{v.Value} }

// PathView reports where the config file lives.
type PathView struct {
	// Path is the absolute path, whether or not the file exists yet.
	Path string `json:"path"`
}

// RenderText implements output.TextRenderer.
func (v PathView) RenderText(_ output.Caps) []string { return []string{v.Path} }

// WriteView confirms a mutation of the config file.
type WriteView struct {
	// Setting is the key that changed.
	Setting string `json:"setting"`
	// Profile is the profile it changed in.
	Profile string `json:"profile"`
	// Action is what happened to it: "set" or "unset".
	Action string `json:"action"`
	// Path is the file that was written.
	Path string `json:"path"`
}

// RenderText implements output.TextRenderer.
func (v WriteView) RenderText(caps output.Caps) []string {
	return []string{caps.Glyphs.OK + " " + v.Action + " " + v.Setting + " in profile " + v.Profile}
}

// ProfileView is one profile in the listing.
type ProfileView struct {
	// Name is the profile's name.
	Name string `json:"name"`
	// Active reports whether it is the one commands use by default.
	Active bool `json:"active"`
	// HasAPIKey reports whether it can reach the REST transport.
	HasAPIKey bool `json:"hasApiKey"`
	// HasSMTP reports whether it can submit over SMTP. The two credentials are separate
	// principals and either may be absent, so a profile is described by both.
	HasSMTP bool `json:"hasSmtp"`
}

// ProfileListView is the whole of `config profile list`.
type ProfileListView struct {
	// Profiles are the stored profiles, sorted by name so the listing is stable.
	Profiles []ProfileView `json:"profiles"`
}

// RenderText implements output.TextRenderer.
func (v ProfileListView) RenderText(caps output.Caps) []string {
	if len(v.Profiles) == 0 {
		return []string{"No profiles configured. Run `mailkube init` to create one."}
	}

	table := output.Table{Headers: []string{"", "PROFILE", "API KEY", "SMTP"}}
	for _, p := range v.Profiles {
		marker := " "
		if p.Active {
			marker = caps.Glyphs.OK
		}
		table.Rows = append(table.Rows, []string{
			marker, p.Name, configured(p.HasAPIKey), configured(p.HasSMTP),
		})
	}
	return table.Lines()
}

// configured renders whether a credential is present, without saying anything about its value.
func configured(ok bool) string {
	if ok {
		return "configured"
	}
	return notSet
}

// ProfileUseView confirms which profile subsequent commands will use.
type ProfileUseView struct {
	// Profile is the newly active profile.
	Profile string `json:"profile"`
}

// RenderText implements output.TextRenderer.
func (v ProfileUseView) RenderText(caps output.Caps) []string {
	return []string{caps.Glyphs.OK + " Active profile is now " + v.Profile}
}

// ProfileDeleteView confirms a profile was removed.
type ProfileDeleteView struct {
	// Profile is the profile that was deleted.
	Profile string `json:"profile"`
}

// RenderText implements output.TextRenderer.
func (v ProfileDeleteView) RenderText(caps output.Caps) []string {
	return []string{caps.Glyphs.OK + " Deleted profile " + v.Profile}
}
