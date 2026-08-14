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
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
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
