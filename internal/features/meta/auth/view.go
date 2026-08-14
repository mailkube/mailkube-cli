package auth

import (
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// maskWith renders a credential so it can be recognised but not used.
func maskWith(ellipsis, secret string) string { return output.Secret(secret, ellipsis) }

// LoginView is the result of storing a credential.
type LoginView struct {
	// Principal is which credential was stored: "api" or "smtp".
	Principal string `json:"principal"`
	// Profile is the profile it was stored in.
	Profile string `json:"profile"`
	// Stored reports whether the file was written.
	Stored bool `json:"stored"`
	// Path is the config file that now holds it.
	Path string `json:"path"`
	// Masked identifies the credential without disclosing it.
	Masked string `json:"masked,omitempty"`
	// Verification is what the credential check learned, if one ran.
	Verification Verification `json:"verification"`
}

// RenderText implements output.TextRenderer.
func (v LoginView) RenderText(caps output.Caps) []string {
	ok := caps.Glyphs.OK
	lines := []string{ok + " Credential format valid (" + v.Masked + ")"}

	switch {
	case v.Verification.Verified:
		lines = append(lines, ok+" Verified — the credential authenticated")
		if v.Verification.Message != "" {
			// The server's own words, rendered as they arrived. This line carries the
			// domain the key is bound to, and paraphrasing it would mean parsing prose
			// for a value and re-deriving it every time the wording changed.
			lines = append(lines, "  "+output.Sanitize(v.Verification.Message))
		}
	case v.Principal == "api":
		lines = append(lines, caps.Glyphs.Warn+" Not verified — the credential was stored unchecked")
	}

	// The SMTP credential goes into a profile that already exists, so naming the file again
	// would repeat a line the user has just read. The API credential is usually the first
	// thing written, and where it landed is worth stating once.
	if v.Principal == "smtp" {
		return append(lines, ok+" Saved to the \""+v.Profile+"\" profile")
	}
	return append(lines, ok+" Saved to "+v.Path+"  (mode 0600, profile \""+v.Profile+"\")")
}

// StatusView reports which credentials are configured.
//
// It describes local state only. Nothing here contacts the platform, so it cannot say whether a
// credential still works — only which one a command would use, and where it came from.
type StatusView struct {
	// Profile is the profile in use.
	Profile string `json:"profile"`
	// APIKey is the masked REST credential, empty when none is configured.
	APIKey string `json:"apiKey,omitempty"`
	// APIKeySource is where it came from.
	APIKeySource string `json:"apiKeySource,omitempty"`
	// SMTPUser is the submission principal, empty when none is configured.
	SMTPUser string `json:"smtpUser,omitempty"`
	// SMTPSource is where it came from.
	SMTPSource string `json:"smtpSource,omitempty"`
	// SMTPPasswordSet reports whether a password is available for it. A username with no
	// password is a half-configured credential, and the two arrive from different places.
	SMTPPasswordSet bool `json:"smtpPasswordSet"`
}

// RenderText implements output.TextRenderer.
func (v StatusView) RenderText(_ output.Caps) []string {
	table := output.Table{Rows: [][]string{{"Profile", v.Profile, ""}}}

	if v.APIKey == "" {
		table.Rows = append(table.Rows, []string{"API key", "not configured", ""})
	} else {
		table.Rows = append(table.Rows, []string{"API key", v.APIKey, v.APIKeySource})
	}

	switch {
	case v.SMTPUser == "":
		table.Rows = append(table.Rows, []string{"SMTP", "not configured", ""})
	case v.SMTPPasswordSet:
		table.Rows = append(table.Rows, []string{"SMTP", v.SMTPUser, v.SMTPSource + "   (password set)"})
	default:
		table.Rows = append(table.Rows, []string{"SMTP", v.SMTPUser, v.SMTPSource + "   (no password)"})
	}

	lines := table.Lines()
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return lines
}

// LogoutView reports which credential was removed.
type LogoutView struct {
	// Principal is which credential was removed: "api" or "smtp".
	Principal string `json:"principal"`
	// Profile is the profile it was removed from, which still exists.
	Profile string `json:"profile"`
}

// RenderText implements output.TextRenderer.
func (v LogoutView) RenderText(caps output.Caps) []string {
	return []string{caps.Glyphs.OK + " Removed the " + v.Principal + " credential from profile " + v.Profile}
}
