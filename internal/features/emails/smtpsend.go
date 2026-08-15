package emails

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// SubmitterFor opens the submission session a send runs on.
//
// The seam the tests substitute, matching the one the API transport has. There is no other way in:
// a command receives a session it did not open, so there is nowhere for a second SMTP client to
// appear.
type SubmitterFor func(ctx context.Context, deps *feature.Deps, config mksmtp.Config) (ports.SMTPSubmitter, error)

// submitOverSMTP builds the message, opens a session and sends it.
func (f *Feature) submitOverSMTP(
	ctx context.Context, deps *feature.Deps, o *sendOptions, p Payload,
) error {
	if err := checkSMTPSupport(p, o.idempotencyKey); err != nil {
		return err
	}

	config, err := f.smtpConfig(deps, o)
	if err != nil {
		return err
	}
	message, err := smtpMessage(deps, p)
	if err != nil {
		return err
	}

	deps.Progress("Submitting to %s as %s…", config.Address(), config.Username)

	session, err := f.submitter(ctx, deps, config)
	if err != nil {
		return err
	}
	defer session.Close()

	if err := session.Send(message); err != nil {
		return err
	}
	return deps.Emit(SubmittedView{
		To:       p.To,
		Host:     config.Address(),
		Username: config.Username,
		TLS:      session.Capabilities().TLSVersion,
	})
}

// submitter opens the session, real unless a test substituted one.
func (f *Feature) submitter(
	ctx context.Context, deps *feature.Deps, config mksmtp.Config,
) (ports.SMTPSubmitter, error) {
	if f.Submitter != nil {
		return f.Submitter(ctx, deps, config)
	}
	return mksmtp.Connect(ctx, config)
}

// smtpConfig resolves the submission credential and where to send it.
//
// There is no fallback to the API key. The two are different principals issued separately, and a
// tool that quietly used one when asked for the other would make "which credential failed" an
// unanswerable question.
func (f *Feature) smtpConfig(deps *feature.Deps, o *sendOptions) (mksmtp.Config, error) {
	resolved, err := deps.Settings(settings.Overrides{
		SMTPUser: o.smtpUser,
		SMTPHost: o.smtpHost,
		SMTPPort: o.smtpPort,
		SMTPTLS:  o.smtpTLS,
	})
	if err != nil {
		return mksmtp.Config{}, err
	}

	username := strings.TrimSpace(resolved.SMTPUser.Value)
	if username == "" {
		return mksmtp.Config{}, errs.Configf(
			"no SMTP credential configured, and --transport smtp needs one.\n" +
				"Run `mailkube auth login --smtp`, or pass --smtp-user.")
	}
	// Shape-checked before a socket is opened. The check is local because the consequence of
	// getting it wrong is not local.
	if err := input.ValidateSMTPUsername(username); err != nil {
		return mksmtp.Config{}, err
	}

	host := strings.TrimSpace(resolved.SMTPHost.Value)
	if host == "" {
		return mksmtp.Config{}, errs.Configf(
			"no SMTP host configured. Pass --smtp-host, or set it with `mailkube config set smtp_host`.")
	}

	port, err := smtpPort(resolved.SMTPPort.Value)
	if err != nil {
		return mksmtp.Config{}, err
	}
	mode, err := smtpTLS(resolved.SMTPTLS.Value)
	if err != nil {
		return mksmtp.Config{}, err
	}

	password, err := f.smtpPassword(deps, resolved)
	if err != nil {
		return mksmtp.Config{}, err
	}

	return mksmtp.Config{
		Host: host, Port: port, TLS: mode,
		Username: username, Password: password,
		Timeout: timeout(resolved.Timeout.Value),
	}, nil
}

// smtpPassword takes the password from the environment, the config file, or a no-echo prompt.
//
// Never from a flag. A password in an argument lands in shell history and in the process list,
// where it outlives the command by a long way.
func (f *Feature) smtpPassword(deps *feature.Deps, resolved settings.Resolved) (string, error) {
	if password := resolved.SMTPPassword.Value; password != "" {
		return password, nil
	}
	return deps.Prompter().Secret(
		"SMTP password",
		errs.Usagef("no SMTP password, and there is no terminal to ask on.\nSet %s.",
			settings.EnvSMTPPassword))
}

// timeout parses the resolved timeout, falling back to the default rather than failing.
//
// The value can only have come from a duration flag or from the settings package's own constant,
// so a parse failure is an impossible state to survive rather than a user error to report.
func timeout(value string) time.Duration {
	d, err := time.ParseDuration(value)
	if err != nil {
		return settings.DefaultTimeout
	}
	return d
}

// smtpPort parses the resolved port.
func smtpPort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port <= 0 || port > 65535 {
		return 0, errs.Configf("%q is not a usable SMTP port", value)
	}
	return port, nil
}

// smtpTLS parses the resolved encryption mode.
func smtpTLS(value string) (mksmtp.TLSMode, error) {
	switch mksmtp.TLSMode(strings.ToLower(strings.TrimSpace(value))) {
	case mksmtp.STARTTLS:
		return mksmtp.STARTTLS, nil
	case mksmtp.Implicit:
		return mksmtp.Implicit, nil
	default:
		return "", errs.Configf(
			"%q is not a usable TLS mode: use starttls or implicit", value)
	}
}

// SubmittedView is what a submission reports back.
//
// It carries less than a REST send does, and that is the transport's own shape rather than an
// omission: submission returns an acceptance, not a record. The id and the Message-ID are assigned
// by the platform and arrive on the webhook events, which is what `webhooks listen` is for.
type SubmittedView struct {
	// To are the recipients the server accepted.
	To []string `json:"to"`
	// Host is where it was submitted.
	Host string `json:"host"`
	// Username is the principal it was submitted as, which is not the from address.
	Username string `json:"username"`
	// TLS is the negotiated channel.
	TLS string `json:"tls,omitempty"`
}

// RenderText implements output.TextRenderer.
func (v SubmittedView) RenderText(caps output.Caps) []string {
	table := output.Table{Rows: [][]string{
		{"to", strings.Join(v.To, ", ")},
		{"via", v.Host + "  (" + v.TLS + ")"},
		{"as", v.Username},
	}}

	lines := []string{caps.Glyphs.OK + " Submitted"}
	for _, row := range table.Lines() {
		lines = append(lines, "  "+row)
	}
	// Said once, here, because it is the question someone asks next and the answer is not the
	// same as it is on the API transport.
	return append(lines, "",
		"  The platform assigns the message id; it arrives on the webhook events.")
}
