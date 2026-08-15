// Package doctor implements `mailkube doctor`: a report on the environment.
//
// It is the command people run repeatedly when something is wrong, which decides two things
// about it. It never sends anything — spending a send-rate slot per run is how a diagnostic
// becomes the problem it was meant to diagnose — and it never fails on a warning: a report that
// exits non-zero because SMTP is unconfigured would be useless to anyone who does not use SMTP.
package doctor

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/buildinfo"
	"github.com/mailkube/mailkube-cli/internal/kernel/clientfactory"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// probeTimeout bounds the one network check, so a firewall that blackholes the connection costs
// a couple of seconds rather than the command's whole timeout.
const probeTimeout = 5 * time.Second

// Reach is the network seam: it reports what the API answered, and when it says it answered.
//
// A function field rather than a call, so the checks can be exercised without a network. This is
// the same shape as the client factory's HTTP seam, and for the same reason: production has no
// switch to weaken, and tests need one to substitute.
type Reach func(ctx context.Context, baseURL string) (Reachability, error)

// Reachability is what one unauthenticated request learned.
type Reachability struct {
	// Latency is how long the round trip took.
	Latency time.Duration
	// ServerTime is the server's own clock, from the Date header, zero when it sent none.
	ServerTime time.Time
}

// Submit opens a submission session for the connectivity probe. The seam a test substitutes.
//
// It is the same shape the SMTP transport uses, and it is deliberately the *unauthenticated*
// probe: this report must stay cheap enough to run in a loop, and a sign-in attempt per run is
// not that. What it answers is the half a user cannot check any other way — that the submission
// port is reachable from here at all, which is the question a corporate network usually decides.
type Submit func(ctx context.Context, config mksmtp.Config) (ports.SMTPSubmitter, error)

// Feature reports on the environment.
type Feature struct {
	// Reach probes the API. Nil means the real one.
	Reach Reach
	// Submit opens a submission connection. Nil means the real one.
	Submit Submit
}

// New returns the doctor feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "doctor" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupSetup,
		Invocation: "doctor",
		Summary:    "Diagnose your environment",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	var offline bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose your environment",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			view, err := f.Run(c.Context(), deps, offline)
			if err != nil {
				return err
			}
			return deps.Emit(view)
		},
	}
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the checks that need a network")
	return cmd
}

// Run performs every check and reports them together.
//
// It returns no error for a failed check. A diagnostic that stopped at the first problem would
// report one thing when the user needs the whole picture, and the picture is the point.
func (f *Feature) Run(ctx context.Context, deps *feature.Deps, offline bool) (ReportView, error) {
	r, err := deps.Settings(settings.Overrides{})
	if err != nil {
		return ReportView{}, err
	}

	view := ReportView{}
	view.add("CLI", f.checkVersions())
	view.add("Config", f.checkConfig(deps))
	view.add("API key", checkAPIKey(r))
	view.add("SMTP", checkSMTP(r))
	if !offline {
		view.add("API", f.checkReachable(ctx, deps, r))
		view.add("Clock skew", f.checkSkew(ctx, deps, r))
		// Only when there is a submission host to probe. An API-only user has no SMTP
		// endpoint, and a row reporting that would be a second warning about the same
		// absence the SMTP row already states.
		if finding, checked := f.checkSubmission(ctx, r); checked {
			view.add("Submission", finding)
		}
	}
	view.add("Proxy", checkProxy(deps))
	view.add("Terminal", checkTerminal(deps))
	return view, nil
}

// checkVersions reports what this binary is.
func (f *Feature) checkVersions() feature.Finding {
	info := buildinfo.Read()
	return ok(info.Version + "  (sdk mailkube-go " + info.SDKVersion + ")")
}

// checkConfig reports where the config lives and whether its permissions are safe.
//
// The permission rule is POSIX-only. Go's Chmod on Windows toggles only the read-only attribute,
// so a mode comparison there can never hold, and writing the check as unconditional would fail
// this report on every Windows machine, every time.
func (f *Feature) checkConfig(deps *feature.Deps) feature.Finding {
	warning, err := deps.Store.PermissionWarning()
	switch {
	case err != nil:
		return fail(err.Error())
	case warning != "":
		return warn(deps.Store.Path() + "  " + warning)
	default:
		return ok(deps.Store.Path())
	}
}

// checkAPIKey reports whether a REST credential is configured, and where it came from.
//
// It does not check whether the credential still works. That is what login is for; re-probing
// here would spend a rate-limit slot every time someone ran a diagnostic.
func checkAPIKey(r settings.Resolved) feature.Finding {
	if !r.APIKey.Set() {
		return warn("not configured — run `mailkube auth login`")
	}
	return ok("profile \"" + r.Profile.Value + "\", " + r.APIKey.Label())
}

// checkSMTP reports the second principal, which is independent of the first.
func checkSMTP(r settings.Resolved) feature.Finding {
	switch {
	case !r.SMTPUser.Set():
		return warn("not configured — needed for --transport smtp")
	case !r.SMTPPassword.Set():
		return warn(r.SMTPUser.Value + " — no password; set " + settings.EnvSMTPPassword)
	default:
		return ok(r.SMTPUser.Value + ", " + r.SMTPUser.Label())
	}
}

// checkReachable reports whether the API answers at all.
//
// It sends no credential and no message: it is an unauthenticated request whose only purpose is
// to learn that the host resolves, the connection completes and TLS verifies.
func (f *Feature) checkReachable(ctx context.Context, deps *feature.Deps, r settings.Resolved) feature.Finding {
	got, err := f.reach(ctx, deps, r)
	if err != nil {
		return fail(err.Error())
	}
	return ok(clientfactory.NormalizeBaseURL(r.BaseURL.Value) + " reachable  " +
		strconv.FormatInt(got.Latency.Milliseconds(), 10) + "ms  (no credentials sent)")
}

// checkSkew compares the local clock against the server's.
//
// It matters because webhook signatures carry a timestamp and are rejected outside a tolerance
// window, so a machine with a wrong clock fails verification for a reason that looks like a
// signing problem. This reuses the same request as the reachability check.
func (f *Feature) checkSkew(ctx context.Context, deps *feature.Deps, r settings.Resolved) feature.Finding {
	got, err := f.reach(ctx, deps, r)
	if err != nil {
		return warn("not measured — the API was not reachable")
	}
	if got.ServerTime.IsZero() {
		return warn("not measured — the server sent no Date header")
	}

	skew := deps.Clock.Now().Sub(got.ServerTime)
	return ok(formatSkew(skew) + "  (webhook tolerance is 300s)")
}

// reach performs the probe through the seam, or through the real client.
func (f *Feature) reach(ctx context.Context, deps *feature.Deps, r settings.Resolved) (Reachability, error) {
	base := clientfactory.NormalizeBaseURL(r.BaseURL.Value)
	if f.Reach != nil {
		return f.Reach(ctx, base)
	}
	return httpReach(ctx, base, deps.Clock.Now)
}

// httpReach is the real probe: one GET, no credential, no body read.
func httpReach(ctx context.Context, baseURL string, now func() time.Time) (Reachability, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return Reachability{}, err
	}

	started := now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Reachability{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	// Any status at all answers the question. This check is about whether the host resolves,
	// the connection completes and the certificate verifies — a 401 proves all three.
	got := Reachability{Latency: now().Sub(started)}
	if date, parseErr := http.ParseTime(resp.Header.Get("Date")); parseErr == nil {
		got.ServerTime = date
	}
	return got, nil
}

// checkSubmission reports whether the configured submission endpoint answers.
//
// It reports whether the row applies at all, so a user who never sends over SMTP is not shown a
// finding about a host they have not configured. Nothing is authenticated and nothing is
// submitted: the connection is opened, the capabilities are read, and it is closed again.
func (f *Feature) checkSubmission(ctx context.Context, r settings.Resolved) (feature.Finding, bool) {
	if !r.SMTPHost.Set() {
		return feature.Finding{}, false
	}

	config, err := submissionConfig(r)
	if err != nil {
		return fail(err.Error()), true
	}

	session, err := f.submit(ctx, config)
	if err != nil {
		return fail(config.Address() + " — " + err.Error()), true
	}
	defer session.Close()

	return ok(config.Address() + " reachable, " + describe(config, session.Capabilities()) +
		"  (no credentials sent)"), true
}

// submissionConfig turns the resolved settings into something dialable.
func submissionConfig(r settings.Resolved) (mksmtp.Config, error) {
	port, err := mksmtp.ParsePort(r.SMTPPort.Value)
	if err != nil {
		return mksmtp.Config{}, err
	}
	mode, err := mksmtp.ParseTLSMode(r.SMTPTLS.Value)
	if err != nil {
		return mksmtp.Config{}, err
	}
	return mksmtp.Config{
		Host: r.SMTPHost.Value, Port: port, TLS: mode, Timeout: probeTimeout,
	}, nil
}

// describe summarises an encrypted submission channel in one clause.
func describe(config mksmtp.Config, caps mksmtp.Capabilities) string {
	encryption := "TLS"
	if config.TLS == mksmtp.STARTTLS {
		encryption = "STARTTLS"
	}
	if caps.TLSVersion != "" {
		encryption += " " + caps.TLSVersion
	}
	return encryption
}

// submit opens the submission connection through the seam, or through the real client.
func (f *Feature) submit(ctx context.Context, config mksmtp.Config) (ports.SMTPSubmitter, error) {
	if f.Submit != nil {
		return f.Submit(ctx, config)
	}
	return mksmtp.Connect(ctx, config)
}

// checkProxy reports the environment that decides whether requests leave this machine at all.
//
// It is here because "it works at home and not at the office" is otherwise unanswerable, and
// because the two transports do not behave alike: Go's HTTP transport honours these variables and
// SMTP does not go through an HTTP proxy at all, which is worth saying before someone spends an
// afternoon on it.
func checkProxy(deps *feature.Deps) feature.Finding {
	var set []string
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, found := deps.Env(key); found && strings.TrimSpace(value) != "" {
			set = append(set, key+"="+value)
		}
	}
	if len(set) == 0 {
		return ok("no proxy or custom CA configured")
	}
	return ok(strings.Join(set, ", ") + "  (SMTP submission does not use an HTTP proxy)")
}

// checkTerminal reports what the CLI concluded about the terminal, which is otherwise invisible
// and is the explanation for output that came out in an unexpected shape.
func checkTerminal(deps *feature.Deps) feature.Finding {
	description := "no terminal (output defaults to JSON)"
	if deps.Caps.TTY {
		description = "terminal, " + strconv.Itoa(deps.Caps.Width) + " cols"
	}
	return ok(description + ", " + glyphSet(deps) + ", " + colour(deps))
}

// glyphSet names which badge set is in use.
func glyphSet(deps *feature.Deps) string {
	if deps.Caps.Unicode {
		return "unicode"
	}
	return "ascii"
}

// colour names whether ANSI colour is being written.
func colour(deps *feature.Deps) string {
	if deps.Caps.Color {
		return "color"
	}
	return "no color"
}

// formatSkew renders a clock difference with its sign, since which way it is wrong matters.
func formatSkew(d time.Duration) string {
	rounded := d.Round(100 * time.Millisecond)
	if rounded >= 0 {
		return "+" + rounded.String()
	}
	return rounded.String()
}

// ok, warn and fail build findings, so the verdict is stated at the call site as one word.
func ok(detail string) feature.Finding {
	return feature.Finding{Status: feature.StatusOK, Detail: detail}
}

func warn(detail string) feature.Finding {
	return feature.Finding{Status: feature.StatusWarn, Detail: detail}
}

func fail(detail string) feature.Finding {
	return feature.Finding{Status: feature.StatusFail, Detail: detail}
}
