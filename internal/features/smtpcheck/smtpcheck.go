// Package smtpcheck implements `mailkube smtp test`.
//
// It answers one question a send cannot: is the path to the submission service sound, and am I
// really talking to it. That second half is why the certificate line reports what the TLS stack
// *verified* rather than what the peer presented — an intercepting gateway on port 587 would
// otherwise produce four green ticks and a name that looks right.
//
// Credentials are opt-in. The default probe puts nothing on the wire that could be rejected,
// because the common case is "can I reach it at all", and a check that signs in every time it runs
// is a check people are told to stop running.
package smtpcheck

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// ConnectFor opens the session the probe inspects. The seam a test substitutes.
type ConnectFor func(ctx context.Context, config mksmtp.Config) (ports.SMTPSubmitter, error)

// Feature tests submission connectivity and credentials.
type Feature struct {
	// Connect opens the session. Nil means the real one.
	Connect ConnectFor
}

// New returns the smtp feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "smtp" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupDevelop,
		Invocation: "smtp test",
		Summary:    "Test SMTP connectivity and credentials",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "smtp",
		Short: "Test SMTP submission",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.testCmd(deps))
	return cmd
}

// options is everything `smtp test` accepts.
type options struct {
	host, port, tls, user string
	auth                  bool
}

// testCmd builds `smtp test`.
func (f *Feature) testCmd(deps *feature.Deps) *cobra.Command {
	o := &options{}

	cmd := &cobra.Command{
		Use:   "test",
		Short: "Test SMTP connectivity, and credentials with --auth",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return f.run(c.Context(), deps, o)
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&o.host, "host", "", "submission host")
	fs.StringVar(&o.port, "port", "", "submission port")
	fs.StringVar(&o.tls, "tls", "", "encryption: starttls or implicit")
	fs.StringVar(&o.user, "user", "", "SMTP username, as localpart@verified-domain")
	fs.BoolVar(&o.auth, "auth", false, "also sign in, testing the credential")
	return cmd
}

// run performs the probe and reports it.
func (f *Feature) run(ctx context.Context, deps *feature.Deps, o *options) error {
	config, err := f.config(deps, o)
	if err != nil {
		return err
	}

	deps.Progress("%s …", config.Address())
	// The injected clock, like everywhere else. Under a fixed one the elapsed time is zero and
	// the report omits it, which is what lets this screen be a golden file at all.
	started := deps.Clock.Now()

	session, err := f.connect(ctx, config)
	if err != nil {
		return err
	}
	defer session.Close()

	return deps.Emit(report(session.Capabilities(), reportInput{
		address:   config.Address(),
		mode:      config.TLS,
		elapsed:   deps.Clock.Now().Sub(started),
		attempted: config.Username != "",
		username:  config.Username,
		now:       deps.Clock.Now(),
	}))
}

// connect opens the session, real unless a test substituted one.
func (f *Feature) connect(ctx context.Context, config mksmtp.Config) (ports.SMTPSubmitter, error) {
	if f.Connect != nil {
		return f.Connect(ctx, config)
	}
	return mksmtp.Connect(ctx, config)
}

// config resolves where to connect and, when asked, what to sign in as.
func (f *Feature) config(deps *feature.Deps, o *options) (mksmtp.Config, error) {
	resolved, err := deps.Settings(settings.Overrides{
		SMTPUser: o.user, SMTPHost: o.host, SMTPPort: o.port, SMTPTLS: o.tls,
	})
	if err != nil {
		return mksmtp.Config{}, err
	}

	host := strings.TrimSpace(resolved.SMTPHost.Value)
	if host == "" {
		return mksmtp.Config{}, errs.Configf(
			"no SMTP host configured. Pass --host, or set it with `mailkube config set smtp_host`.")
	}

	port, err := parsePort(resolved.SMTPPort.Value)
	if err != nil {
		return mksmtp.Config{}, err
	}
	mode, err := parseTLS(resolved.SMTPTLS.Value)
	if err != nil {
		return mksmtp.Config{}, err
	}

	config := mksmtp.Config{
		Host: host, Port: port, TLS: mode, Timeout: 30 * time.Second,
	}
	if !o.auth {
		// No credential in the configuration means none on the wire, which is the whole of
		// what makes the default probe safe to run repeatedly.
		return config, nil
	}
	return f.withCredential(deps, resolved, config)
}

// withCredential adds the sign-in details, refusing a username that cannot be right.
func (f *Feature) withCredential(
	deps *feature.Deps, resolved settings.Resolved, config mksmtp.Config,
) (mksmtp.Config, error) {
	username := strings.TrimSpace(resolved.SMTPUser.Value)
	if username == "" {
		return mksmtp.Config{}, errs.Configf(
			"--auth needs a username. Pass --user, or run `mailkube auth login --smtp`.")
	}
	// Checked before a socket is opened, because the consequence of getting it wrong is not
	// local to this command.
	if err := input.ValidateSMTPUsername(username); err != nil {
		return mksmtp.Config{}, err
	}

	password := resolved.SMTPPassword.Value
	if password == "" {
		asked, err := deps.Prompter().Secret(
			"SMTP password",
			errs.Usagef("no SMTP password, and there is no terminal to ask on.\nSet %s.",
				settings.EnvSMTPPassword))
		if err != nil {
			return mksmtp.Config{}, err
		}
		password = asked
	}

	config.Username, config.Password = username, password
	return config, nil
}

// parsePort reads the resolved port, as a configuration problem when it is not one.
func parsePort(value string) (int, error) {
	port, err := mksmtp.ParsePort(value)
	if err != nil {
		return 0, errs.Configf("%s", err)
	}
	return port, nil
}

// parseTLS reads the resolved encryption mode, as a configuration problem when it is not one.
func parseTLS(value string) (mksmtp.TLSMode, error) {
	mode, err := mksmtp.ParseTLSMode(value)
	if err != nil {
		return "", errs.Configf("%s", err)
	}
	return mode, nil
}
