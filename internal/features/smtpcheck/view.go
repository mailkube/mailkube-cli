package smtpcheck

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// reportInput is what the view needs beyond the server's own capabilities.
type reportInput struct {
	address   string
	mode      mksmtp.TLSMode
	elapsed   time.Duration
	attempted bool
	username  string
	now       time.Time
}

// CheckView is one line of the report.
type CheckView struct {
	// Label is what was checked.
	Label string `json:"label"`
	// Detail is what was found.
	Detail string `json:"detail"`
}

// ReportView is the whole probe.
type ReportView struct {
	// Address is what was probed.
	Address string `json:"address"`
	// Checks are the findings, in the order the conversation reached them.
	Checks []CheckView `json:"checks"`
	// Authenticated reports whether a credential was tested.
	Authenticated bool `json:"authenticated"`
}

// report builds the view from what the session learned.
//
// Every value here is derived by this program: the parsed capability tokens, the negotiated
// version and cipher, and the verified certificate. The server's free-text greeting is not among
// them — it is the server's to write, and the CLI has no business rewriting it.
func report(caps mksmtp.Capabilities, in reportInput) ReportView {
	view := ReportView{Address: in.address, Authenticated: in.attempted}

	view.Checks = append(view.Checks, CheckView{"TCP", "connected" + elapsed(in.elapsed)})
	view.Checks = append(view.Checks, CheckView{"EHLO", strings.Join(tokens(caps), " ")})

	if caps.TLSVersion != "" {
		label := "STARTTLS"
		if in.mode == mksmtp.Implicit {
			label = "TLS"
		}
		view.Checks = append(view.Checks, CheckView{label, caps.TLSVersion + ", " + caps.CipherSuite})
	}
	if caps.CertificateSubject != "" {
		view.Checks = append(view.Checks, CheckView{"Certificate", certificate(caps, in.now)})
	}
	if in.attempted {
		view.Checks = append(view.Checks, CheckView{"AUTH", "accepted as " + in.username})
	}
	return view
}

// tokens renders the advertised capabilities as the registered keywords they are.
//
// Keywords rather than the raw EHLO text: these are protocol tokens this program parsed and
// understood, which is a different thing from repeating whatever the server chose to say.
func tokens(caps mksmtp.Capabilities) []string {
	var listed []string
	if caps.StartTLS {
		listed = append(listed, "STARTTLS")
	}
	if caps.Pipelining {
		listed = append(listed, "PIPELINING")
	}
	if caps.EightBitMIME {
		listed = append(listed, "8BITMIME")
	}
	if caps.MaxSize > 0 {
		listed = append(listed, "SIZE="+strconv.FormatInt(caps.MaxSize, 10))
	}
	if len(caps.Auth) > 0 {
		listed = append(listed, "AUTH="+strings.Join(caps.Auth, ","))
	}
	return listed
}

// certificate describes the verified peer and how long it is good for.
func certificate(caps mksmtp.Capabilities, now time.Time) string {
	detail := "valid for " + caps.CertificateSubject
	if caps.CertificateIssuer != "" {
		detail += ", issued by " + caps.CertificateIssuer
	}
	if caps.CertificateExpiry.IsZero() {
		return detail
	}

	detail += ", expires " + caps.CertificateExpiry.UTC().Format("2006-01-02")
	if now.IsZero() {
		return detail
	}
	return detail + " (" + remaining(caps.CertificateExpiry.Sub(now)) + ")"
}

// remaining renders how much life a certificate has left.
func remaining(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days < 0:
		return "expired"
	case days == 1:
		return "1 day"
	default:
		return strconv.Itoa(days) + " days"
	}
}

// elapsed renders a duration, or nothing when no clock was available.
func elapsed(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%d ms)", d.Milliseconds())
}

// RenderText implements output.TextRenderer.
func (v ReportView) RenderText(caps output.Caps) []string {
	table := output.Table{}
	for _, check := range v.Checks {
		table.Rows = append(table.Rows, []string{caps.Glyphs.OK + " " + check.Label, check.Detail})
	}

	lines := table.Lines()
	if v.Authenticated {
		return append(lines, "", "The credential works.")
	}
	// The consequence, not the mechanism. Someone needs to know that repeating this in a loop
	// is a bad idea; nobody needs to know what enforces that.
	return append(lines, "",
		"Connectivity is fine. No credential was sent.",
		"Add --auth to test one. Sign-in is rate-limited, so do not run it in a loop.")
}
