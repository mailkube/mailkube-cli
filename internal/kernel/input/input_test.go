package input_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
)

func TestResolveTreatsAPlainValueAsContent(t *testing.T) {
	t.Parallel()

	got, err := input.NewReader(strings.NewReader("")).Resolve("Thanks for signing up.")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "Thanks for signing up." {
		t.Errorf("Resolve() = %q", got)
	}
}

func TestResolveReadsAFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "body.html")
	if err := os.WriteFile(path, []byte("<p>hello</p>"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := input.NewReader(strings.NewReader("")).Resolve("@" + path)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "<p>hello</p>" {
		t.Errorf("Resolve() = %q", got)
	}
}

func TestResolveReadsStandardInput(t *testing.T) {
	t.Parallel()

	got, err := input.NewReader(strings.NewReader("piped body")).Resolve("@-")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "piped body" {
		t.Errorf("Resolve() = %q", got)
	}
}

// Without the escape there is no way to pass a value that legitimately begins with @, and an email
// address is the obvious one.
func TestResolveUnescapesALiteralAt(t *testing.T) {
	t.Parallel()

	got, err := input.NewReader(strings.NewReader("")).Resolve(`\@example.com`)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "@example.com" {
		t.Errorf("Resolve() = %q, want the literal value", got)
	}
}

// Standard input is a single stream. A second read would silently return nothing, and a command
// that quietly sends an empty body is worse than one that refuses to run.
func TestResolveRefusesASecondStdinReference(t *testing.T) {
	t.Parallel()

	r := input.NewReader(strings.NewReader("piped body"))
	if _, err := r.Resolve("@-"); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}

	_, err := r.Resolve("@-")
	if err == nil {
		t.Fatal("the second @- was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
	}
}

func TestResolveReportsAMissingFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "nope.json")

	_, err := input.NewReader(strings.NewReader("")).Resolve("@" + missing)
	if err == nil {
		t.Fatal("a missing file was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", got, errs.CodeValidation)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestResolveRejectsABareAt(t *testing.T) {
	t.Parallel()

	_, err := input.NewReader(strings.NewReader("")).Resolve("@")
	if err == nil {
		t.Fatal("a bare @ was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
	}
}

func TestResolvePropagatesAReadFailure(t *testing.T) {
	t.Parallel()

	_, err := input.NewReader(failingReader{}).Resolve("@-")
	if err == nil {
		t.Fatal("a failed read was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeValidation {
		t.Errorf("exit code = %d, want %d", got, errs.CodeValidation)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("the pipe broke") }

func TestParseAtAcceptsEveryUnambiguousSpelling(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 14, 7, 32, 0, 0, time.UTC)

	tests := []struct {
		name  string
		value string
		want  time.Time
	}{
		{"relative hours", "+2h", now.Add(2 * time.Hour)},
		{"relative compound", "+1h30m", now.Add(90 * time.Minute)},
		{"relative minutes", "+30m", now.Add(30 * time.Minute)},
		{
			"rfc 3339 utc",
			"2026-09-01T09:00:00Z",
			time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC),
		},
		{
			"rfc 3339 with offset",
			"2026-09-01T09:00:00+02:00",
			time.Date(2026, time.September, 1, 7, 0, 0, 0, time.UTC),
		},
		{
			"wall time with a spaced offset",
			"2026-09-01 09:00 +02:00",
			time.Date(2026, time.September, 1, 7, 0, 0, 0, time.UTC),
		},
		{
			"wall time with seconds",
			"2026-09-01 09:00:30 +02:00",
			time.Date(2026, time.September, 1, 7, 0, 30, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := input.ParseAt(tc.value, now)
			if err != nil {
				t.Fatalf("ParseAt(%q): %v", tc.value, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("ParseAt(%q) = %v, want %v", tc.value, got.UTC(), tc.want)
			}
		})
	}
}

// The one input whose meaning changes with where it is read. Around a daylight-saving boundary it
// can name an instant that happens twice or not at all, so it is refused rather than guessed.
func TestParseAtRefusesANaiveWallTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 14, 7, 32, 0, 0, time.UTC)

	for _, value := range []string{"2026-09-01 09:00", "2026-09-01T09:00:00", "2026-09-01 09:00:30"} {
		_, err := input.ParseAt(value, now)
		if err == nil {
			t.Errorf("ParseAt(%q) was accepted", value)
			continue
		}
		if got := errs.CodeFor(err); got != errs.CodeUsage {
			t.Errorf("ParseAt(%q) exit code = %d, want %d", value, got, errs.CodeUsage)
		}
		if !strings.Contains(err.Error(), "offset") {
			t.Errorf("ParseAt(%q) does not name the missing offset: %v", value, err)
		}
	}
}

func TestParseAtRejectsTheUnusable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 14, 7, 32, 0, 0, time.UTC)

	tests := []struct {
		name  string
		value string
		want  errs.Code
	}{
		{"empty", "", errs.CodeUsage},
		{"blank", "   ", errs.CodeUsage},
		{"not a time", "next tuesday", errs.CodeValidation},
		{"malformed duration", "+2 hours", errs.CodeValidation},
		{"zero offset", "+0s", errs.CodeValidation},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := input.ParseAt(tc.value, now); err == nil {
				t.Fatalf("ParseAt(%q) was accepted", tc.value)
			} else if got := errs.CodeFor(err); got != tc.want {
				t.Errorf("ParseAt(%q) exit code = %d, want %d (%v)", tc.value, got, tc.want, err)
			}
		})
	}
}

func TestPairFlagCollectsInOrder(t *testing.T) {
	t.Parallel()

	f := input.NewTagFlag()
	for _, raw := range []string{"campaign=launch", "cohort=q3", "campaign=second"} {
		if err := f.Set(raw); err != nil {
			t.Fatalf("Set(%q): %v", raw, err)
		}
	}

	want := []input.Pair{
		{Key: "campaign", Value: "launch"},
		{Key: "cohort", Value: "q3"},
		{Key: "campaign", Value: "second"},
	}
	got := f.Pairs()
	if len(got) != len(want) {
		t.Fatalf("got %d pairs, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pair %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Only the first separator splits. A header value is free to contain a colon and a template
// variable is free to contain an equals sign; neither should need escaping to say so.
func TestPairFlagSplitsOnTheFirstSeparatorOnly(t *testing.T) {
	t.Parallel()

	header := input.NewHeaderFlag()
	if err := header.Set("X-Report-To: https://example.com/hook?a=1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := header.Pairs()[0]; got.Key != "X-Report-To" || got.Value != "https://example.com/hook?a=1" {
		t.Errorf("pair = %+v", got)
	}

	v := input.NewVarFlag()
	if err := v.Set("equation=a=b+c"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := v.Pairs()[0]; got.Key != "equation" || got.Value != "a=b+c" {
		t.Errorf("pair = %+v", got)
	}
}

func TestPairFlagRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{"no separator", "campaign"},
		{"empty name", "=launch"},
		{"blank name", "   =launch"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := input.NewTagFlag().Set(tc.raw)
			if err == nil {
				t.Fatalf("Set(%q) was accepted", tc.raw)
			}
			if got := errs.CodeFor(err); got != errs.CodeUsage {
				t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
			}
			if !strings.Contains(err.Error(), "--tag") {
				t.Errorf("the error shows no example: %v", err)
			}
		})
	}
}

// An empty value is legitimate: clearing a template variable is a thing a user may mean.
func TestPairFlagAcceptsAnEmptyValue(t *testing.T) {
	t.Parallel()

	f := input.NewVarFlag()
	if err := f.Set("nickname="); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := f.Pairs()[0]; got.Key != "nickname" || got.Value != "" {
		t.Errorf("pair = %+v", got)
	}
}

// The claim that PairFlag is usable as a flag, checked by the compiler. The assertion lives in
// the test rather than the package so kernel/input itself stays free of a flag-library dependency.
var _ pflag.Value = (*input.PairFlag)(nil)

func TestPairFlagSatisfiesPflag(t *testing.T) {
	t.Parallel()

	f := input.NewTagFlag()
	if got := f.Type(); got != "tag" {
		t.Errorf("Type() = %q", got)
	}
	if got := f.String(); got != "" {
		t.Errorf("String() on an unset flag = %q, want empty", got)
	}

	_ = f.Set("campaign=launch")
	_ = f.Set("cohort=q3")
	if got, want := f.String(), "campaign=launch,cohort=q3"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseAtRefusesATimeThatHasAlreadyPassed(t *testing.T) {
	t.Parallel()

	// The relative form has always refused a non-positive offset. An absolute one in the past is
	// the same mistake in a different spelling, and accepting it would send a scheduled message
	// the server can only reject — after the round trip. The pathological case is
	// "0001-01-01 0:00+00:00", which parses to the zero time, and the zero time is what every
	// other part of this program reads as "no time given".
	now := time.Date(2026, time.August, 14, 7, 32, 0, 0, time.UTC)

	for _, value := range []string{
		"2020-01-01T00:00:00Z",
		"0001-01-01 0:00+00:00",
		"2026-08-14T07:32:00Z", // now itself: a schedule for this instant is not a schedule
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			at, err := input.ParseAt(value, now)
			if err == nil {
				t.Fatalf("ParseAt(%q) accepted a time that is not in the future: %s", value, at)
			}
			if !strings.Contains(err.Error(), "not in the future") {
				t.Errorf("message = %q, want it to say why", err)
			}
			if errs.CodeFor(err) != errs.CodeValidation {
				t.Errorf("exit code = %d, want %d", errs.CodeFor(err), errs.CodeValidation)
			}
		})
	}
}
