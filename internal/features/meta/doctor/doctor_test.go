package doctor_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mailkube/mailkube-cli/internal/features/meta/doctor"
	"github.com/mailkube/mailkube-cli/internal/kernel/clock"
	"github.com/mailkube/mailkube-cli/internal/kernel/configstore"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/ports"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// reaching returns a probe that answers with a fixed reachability.
func reaching(r doctor.Reachability) doctor.Reach {
	return func(context.Context, string) (doctor.Reachability, error) { return r, nil }
}

// find returns one check from a report by label.
func find(t *testing.T, view doctor.ReportView, label string) doctor.CheckView {
	t.Helper()
	for _, c := range view.Checks {
		if c.Label == label {
			return c
		}
	}
	t.Fatalf("no check labelled %q in the report", label)
	return doctor.CheckView{}
}

func TestAnEmptyEnvironmentWarnsAboutBothCredentialsAndFailsNothing(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	view, err := doctor.New().Run(context.Background(), deps, true)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	// Two independent principals, two rows: a user who only sends over REST must be able to see
	// that the missing SMTP credential is not their problem.
	if got := find(t, view, "API key").Status; got != "warn" {
		t.Errorf("API key status = %q, want warn", got)
	}
	if got := find(t, view, "SMTP").Status; got != "warn" {
		t.Errorf("SMTP status = %q, want warn", got)
	}
	// Nothing failed, and nothing should: an unconfigured credential is a report, not a fault.
	if view.Failures != 0 {
		t.Errorf("failures = %d, want 0", view.Failures)
	}
	if view.Warnings != 2 {
		t.Errorf("warnings = %d, want 2", view.Warnings)
	}
}

func TestOfflineSkipsEveryNetworkCheck(t *testing.T) {
	t.Parallel()

	probed := false
	f := doctor.New()
	f.Reach = func(context.Context, string) (doctor.Reachability, error) {
		probed = true
		return doctor.Reachability{}, nil
	}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	if _, err := f.Run(context.Background(), deps, true); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if probed {
		t.Error("--offline made a network request")
	}
}

func TestClockSkewIsReportedAgainstTheServersOwnTime(t *testing.T) {
	t.Parallel()

	// The local clock is fixed by the test harness; the server is reported as four seconds
	// behind it. Skew matters because a webhook signature carries a timestamp and is rejected
	// outside a tolerance window, so a wrong clock looks like a signing problem.
	local := clock.Testing().Now()

	f := doctor.New()
	f.Reach = reaching(doctor.Reachability{
		Latency:    142 * time.Millisecond,
		ServerTime: local.Add(-4 * time.Second),
	})

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	view, err := f.Run(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	skew := find(t, view, "Clock skew")
	if !strings.Contains(skew.Detail, "+4s") {
		t.Errorf("skew detail = %q, want it to name +4s", skew.Detail)
	}
	if reach := find(t, view, "API"); !strings.Contains(reach.Detail, "142ms") {
		t.Errorf("reachability detail = %q, want it to name the latency", reach.Detail)
	}
}

func TestAnUnreachableAPIFailsWithoutStoppingTheReport(t *testing.T) {
	t.Parallel()

	f := doctor.New()
	f.Reach = func(context.Context, string) (doctor.Reachability, error) {
		return doctor.Reachability{}, errors.New("dial tcp: connection refused")
	}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	view, err := f.Run(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("a failed check ended the whole report: %v", err)
	}

	if got := find(t, view, "API").Status; got != "fail" {
		t.Errorf("API status = %q, want fail", got)
	}
	// Skew cannot be measured without a reachable server, and saying so is more useful than
	// reporting a skew of zero.
	if got := find(t, view, "Clock skew").Status; got != "warn" {
		t.Errorf("skew status = %q, want warn", got)
	}
	// The rest of the report still ran: the picture is the point.
	if find(t, view, "Terminal").Status != "ok" {
		t.Error("a network failure suppressed the local checks")
	}
}

func TestASkewCheckWithNoServerDateSaysSoRatherThanGuessing(t *testing.T) {
	t.Parallel()

	f := doctor.New()
	f.Reach = reaching(doctor.Reachability{Latency: time.Millisecond})

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	view, err := f.Run(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	skew := find(t, view, "Clock skew")
	if skew.Status != "warn" || !strings.Contains(skew.Detail, "no Date header") {
		t.Errorf("skew = %q / %q, want a warning naming the missing header", skew.Status, skew.Detail)
	}
}

func TestAConfiguredEnvironmentReportsBothCredentials(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{
			settings.EnvAPIKey:       "mk_from_env",
			settings.EnvSMTPPassword: "secret",
		},
	})
	// The SMTP username comes from the profile, so a run with only a password set is the
	// half-configured case: a password with no username is not a credential.
	view, err := doctor.New().Run(context.Background(), deps, true)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	if got := find(t, view, "API key").Status; got != "ok" {
		t.Errorf("API key status = %q, want ok", got)
	}
	if got := find(t, view, "SMTP").Status; got != "warn" {
		t.Errorf("SMTP status = %q, want warn", got)
	}
}

func TestTheSummaryAgreesWithTheRows(t *testing.T) {
	t.Parallel()

	// The counts are maintained as checks are added rather than recomputed at render time, and
	// this is the assertion that keeps that honest: a summary that disagreed with its own rows
	// is the one thing a reader of this screen would not think to check.
	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	view, err := doctor.New().Run(context.Background(), deps, true)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	warnings, failures := 0, 0
	for _, c := range view.Checks {
		switch c.Status {
		case "warn":
			warnings++
		case "fail":
			failures++
		}
	}
	if warnings != view.Warnings || failures != view.Failures {
		t.Errorf("summary says %d warnings and %d failures; the rows say %d and %d",
			view.Warnings, view.Failures, warnings, failures)
	}

	rendered := strings.Join(view.RenderText(output.Caps{Glyphs: output.ASCIIGlyphs()}), "\n")
	if !strings.Contains(rendered, "2 warnings.") {
		t.Errorf("the rendered summary does not state the count:\n%s", rendered)
	}
}

func TestTheRealProbeReadsLatencyAndTheServersClock(t *testing.T) {
	t.Parallel()

	// The default probe is the one that actually ships, so it gets a server to talk to rather
	// than being left as the one path nothing exercises. It sends no credential and reads no
	// body: any status at all answers the question it asks.
	served := time.Date(2026, 8, 14, 9, 32, 0, 0, time.UTC)
	requests := 0
	var authorization string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Date", served.Format(http.TimeFormat))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Globals: &settings.Globals{BaseURL: server.URL + "/mta/v1/"},
	})
	view, err := doctor.New().Run(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	if requests == 0 {
		t.Fatal("the probe made no request")
	}
	if authorization != "" {
		t.Errorf("the probe sent a credential: %q", authorization)
	}
	if got := find(t, view, "API").Status; got != "ok" {
		t.Errorf("a 401 was not read as reachable: %q", got)
	}
	if got := find(t, view, "Clock skew").Status; got != "ok" {
		t.Errorf("skew was not measured from the Date header: %q", got)
	}
}

// withSubmissionHost writes a profile carrying a submission endpoint, which is the only way this
// report learns of one: doctor takes no flags of its own, so the host comes from configuration.
func withSubmissionHost(t *testing.T, deps *feature.Deps, host string) {
	t.Helper()

	err := deps.Store.Update(func(c *configstore.Config) error {
		c.Profiles = map[string]configstore.Profile{
			settings.DefaultProfile: {SMTP: &configstore.SMTP{
				Username: "app01@acme.com", Host: host,
			}},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("writing the test profile: %v", err)
	}
}

func TestSubmissionIsNotReportedWhenThereIsNoHostToProbe(t *testing.T) {
	t.Parallel()

	// An API-only user has no submission endpoint, and a row about a host they never configured
	// would be a second warning about the absence the SMTP row already states.
	probed := false
	f := doctor.New()
	f.Submit = func(context.Context, mksmtp.Config) (ports.SMTPSubmitter, error) {
		probed = true
		return &fakeSession{}, nil
	}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	f.Reach = reaching(doctor.Reachability{Latency: time.Millisecond})

	view, err := f.Run(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if probed {
		t.Error("a submission connection was opened with no host configured")
	}
	for _, c := range view.Checks {
		if c.Label == "Submission" {
			t.Errorf("the report carries a submission row with nothing to probe: %q", c.Detail)
		}
	}
}

func TestSubmissionIsProbedWithoutACredential(t *testing.T) {
	t.Parallel()

	var used mksmtp.Config
	session := &fakeSession{caps: mksmtp.Capabilities{StartTLS: true, TLSVersion: "TLS 1.3"}}

	f := doctor.New()
	f.Reach = reaching(doctor.Reachability{Latency: time.Millisecond})
	f.Submit = func(_ context.Context, config mksmtp.Config) (ports.SMTPSubmitter, error) {
		used = config
		return session, nil
	}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{settings.EnvSMTPPassword: "secret"},
	})
	withSubmissionHost(t, deps, "smtp.mailkube.com")

	view, err := f.Run(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	// A diagnostic people run in a loop must not sign in on every run, and a password being
	// available is not a reason to use it: what this row answers is whether the port is
	// reachable from here, which is the half a corporate network usually decides.
	if used.Username != "" || used.Password != "" {
		t.Errorf("the probe carried a credential: %+v", used)
	}
	submission := find(t, view, "Submission")
	if submission.Status != "ok" {
		t.Errorf("submission status = %q, want ok", submission.Status)
	}
	for _, want := range []string{"smtp.mailkube.com:587", "STARTTLS TLS 1.3", "no credentials sent"} {
		if !strings.Contains(submission.Detail, want) {
			t.Errorf("submission detail = %q, want it to name %q", submission.Detail, want)
		}
	}
	if !session.closed {
		t.Error("the probe left the session open")
	}
}

func TestAnUnreachableSubmissionPortFailsItsOwnRowOnly(t *testing.T) {
	t.Parallel()

	f := doctor.New()
	f.Reach = reaching(doctor.Reachability{Latency: time.Millisecond})
	f.Submit = func(context.Context, mksmtp.Config) (ports.SMTPSubmitter, error) {
		return nil, errors.New("dial tcp: i/o timeout")
	}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	withSubmissionHost(t, deps, "smtp.mailkube.com")

	view, err := f.Run(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	submission := find(t, view, "Submission")
	if submission.Status != "fail" {
		t.Errorf("submission status = %q, want fail", submission.Status)
	}
	// The address is in the message because a blocked port is usually a firewall, and the first
	// thing anyone asks is which port.
	if !strings.Contains(submission.Detail, "smtp.mailkube.com:587") {
		t.Errorf("submission detail = %q, want it to name what could not be reached", submission.Detail)
	}
	if find(t, view, "Terminal").Status != "ok" {
		t.Error("a submission failure suppressed the local checks")
	}
}

func TestOfflineSkipsTheSubmissionProbeToo(t *testing.T) {
	t.Parallel()

	probed := false
	f := doctor.New()
	f.Submit = func(context.Context, mksmtp.Config) (ports.SMTPSubmitter, error) {
		probed = true
		return &fakeSession{}, nil
	}

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{})
	withSubmissionHost(t, deps, "smtp.mailkube.com")

	if _, err := f.Run(context.Background(), deps, true); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if probed {
		t.Error("--offline opened a submission connection")
	}
}

func TestTheProxyRowReportsWhatDecidesWhetherRequestsLeaveTheMachine(t *testing.T) {
	t.Parallel()

	deps, _, _ := testsupport.TestDeps(t, testsupport.TestOptions{
		Env: map[string]string{
			"HTTPS_PROXY":   "http://proxy.corp:3128",
			"SSL_CERT_FILE": "/etc/ssl/corp.pem",
			"NO_PROXY":      "",
		},
	})
	view, err := doctor.New().Run(context.Background(), deps, true)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}

	proxy := find(t, view, "Proxy")
	for _, want := range []string{"HTTPS_PROXY=http://proxy.corp:3128", "SSL_CERT_FILE=/etc/ssl/corp.pem"} {
		if !strings.Contains(proxy.Detail, want) {
			t.Errorf("proxy detail = %q, want it to name %q", proxy.Detail, want)
		}
	}
	// A variable that is set to nothing is not configuration, and listing it would send someone
	// looking for a proxy that is not there.
	if strings.Contains(proxy.Detail, "NO_PROXY") {
		t.Errorf("an empty variable was reported as configured: %q", proxy.Detail)
	}
	// Stated because the two transports do not behave alike, and an afternoon can go into
	// discovering that the proxy the API uses is not on the submission path at all.
	if !strings.Contains(proxy.Detail, "SMTP submission does not use an HTTP proxy") {
		t.Errorf("proxy detail = %q, want it to say SMTP does not use one", proxy.Detail)
	}
	// It is never a warning: a proxy is an ordinary way to be configured, not a fault.
	if proxy.Status != "ok" {
		t.Errorf("proxy status = %q, want ok", proxy.Status)
	}
}

// fakeSession is a submission connection that answers with fixed capabilities.
type fakeSession struct {
	caps   mksmtp.Capabilities
	closed bool
}

func (f *fakeSession) Send(mksmtp.Message) error         { return nil }
func (f *fakeSession) Capabilities() mksmtp.Capabilities { return f.caps }
func (f *fakeSession) Close()                            { f.closed = true }

func TestTheVerdictIsWhatMakesTheReportUsableInAScript(t *testing.T) {
	t.Parallel()

	// `mailkube doctor && deploy` is the reason this exists. A report that always exits 0
	// tells that shell to carry on past a broken configuration, which is the one thing the
	// command was run to prevent.
	tests := []struct {
		name   string
		view   doctor.ReportView
		strict bool
		want   errs.Code
		says   string
	}{
		{
			name: "a clean report",
			view: doctor.ReportView{},
			want: errs.CodeOK,
		},
		{
			// An unconfigured credential is a report, not a fault. Failing here would make
			// the diagnostic unpassable for the many people who never use SMTP, and a
			// check nobody can pass is a check everybody learns to ignore.
			name: "warnings alone",
			view: doctor.ReportView{Warnings: 2},
			want: errs.CodeOK,
		},
		{
			name:   "warnings under --strict",
			view:   doctor.ReportView{Warnings: 2},
			strict: true,
			want:   errs.CodeConfig,
			says:   "--strict",
		},
		{
			name: "one failed check",
			view: doctor.ReportView{Failures: 1, Warnings: 3},
			want: errs.CodeConfig,
			says: "1 check failed",
		},
		{
			// One code for any number of failures. Deriving it from whichever check failed
			// would mean picking one when several did, and the rule for picking would end
			// up being the order the checks happen to run in.
			name: "several failed checks",
			view: doctor.ReportView{Failures: 3},
			want: errs.CodeConfig,
			says: "3 checks failed",
		},
		{
			name:   "failures outrank --strict warnings",
			view:   doctor.ReportView{Failures: 1, Warnings: 5},
			strict: true,
			want:   errs.CodeConfig,
			says:   "1 check failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.view.Verdict(tt.strict)
			if got := errs.CodeFor(err); got != tt.want {
				t.Errorf("exit code = %d, want %d (%v)", got, tt.want, err)
			}
			if tt.says == "" {
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("the verdict does not say %q: %v", tt.says, err)
			}
		})
	}
}
