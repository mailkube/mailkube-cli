package output_test

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

// buffers stand in for streams that are not terminals, which is the state every test runs in and
// every CI job runs in.
func buffers() (*bytes.Buffer, *bytes.Buffer) {
	return &bytes.Buffer{}, &bytes.Buffer{}
}

func TestDetectTreatsANonTerminalAsNonInteractive(t *testing.T) {
	t.Parallel()

	in, out := buffers()
	caps := output.Detect(in, out, output.MapEnv(nil))

	if caps.TTY {
		t.Error("a buffer was reported as a terminal")
	}
	if caps.Interactive {
		t.Error("a buffer was reported as interactive")
	}
	if caps.Color {
		t.Error("colour was enabled with no terminal and no override")
	}
	if caps.Width != output.DefaultWidth {
		t.Errorf("Width = %d, want %d", caps.Width, output.DefaultWidth)
	}
}

// NO_COLOR and CLICOLOR_FORCE are conventions shared across tools: a user who has set one has
// already answered this question for their whole machine.
func TestDetectHonoursTheColourConventions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"no override", nil, false},
		{"forced on", map[string]string{"CLICOLOR_FORCE": "1"}, true},
		{"forced on, then off", map[string]string{"CLICOLOR_FORCE": "1", "NO_COLOR": "1"}, false},
		{"force set to zero is not a force", map[string]string{"CLICOLOR_FORCE": "0"}, false},
		{"dumb terminal", map[string]string{"CLICOLOR_FORCE": "1", "TERM": "dumb"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in, out := buffers()
			if got := output.Detect(in, out, output.MapEnv(tc.env)).Color; got != tc.want {
				t.Errorf("Color = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectResolvesTheGlyphSet(t *testing.T) {
	t.Parallel()

	// With no locale variable at all the answer is platform-specific, and deliberately so: a
	// terminal that names no encoding is assumed to be UTF-8, except on Windows, where naming no
	// encoding is the norm and the legacy code page genuinely cannot draw these glyphs. The two
	// cases below that say nothing about a locale take that answer; every other case states an
	// encoding and means the same thing everywhere.
	unstated := runtime.GOOS != "windows"

	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing said", nil, unstated},
		{"windows terminal announces itself", map[string]string{"WT_SESSION": "1"}, true},
		{"utf-8 locale", map[string]string{"LANG": "en_GB.UTF-8"}, true},
		{"utf8 locale", map[string]string{"LC_ALL": "C.utf8"}, true},
		{"posix locale", map[string]string{"LANG": "C"}, false},
		{"latin-1 locale", map[string]string{"LC_CTYPE": "en_US.ISO8859-1"}, false},
		{"forced ascii", map[string]string{"LANG": "en_GB.UTF-8", "MAILKUBE_ASCII": "1"}, false},
		{"ascii set to zero is not a force", map[string]string{"MAILKUBE_ASCII": "0"}, unstated},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in, out := buffers()
			caps := output.Detect(in, out, output.MapEnv(tc.env))

			if caps.Unicode != tc.want {
				t.Errorf("Unicode = %v, want %v", caps.Unicode, tc.want)
			}
			// The glyph set must follow the capability. A unicode badge on an ascii
			// terminal is the one thing this whole mechanism exists to prevent.
			wantOK := output.ASCIIGlyphs().OK
			if tc.want {
				wantOK = output.UnicodeGlyphs().OK
			}
			if caps.Glyphs.OK != wantOK {
				t.Errorf("Glyphs.OK = %q, want %q", caps.Glyphs.OK, wantOK)
			}
		})
	}
}

func TestDetectReadsTheWidthFromTheEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want int
	}{
		{"stated", map[string]string{"COLUMNS": "120"}, 120},
		{"unparseable", map[string]string{"COLUMNS": "wide"}, output.DefaultWidth},
		{"zero", map[string]string{"COLUMNS": "0"}, output.DefaultWidth},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in, out := buffers()
			if got := output.Detect(in, out, output.MapEnv(tc.env)).Width; got != tc.want {
				t.Errorf("Width = %d, want %d", got, tc.want)
			}
		})
	}
}

// Every case here arrives as an ordinary string field in an API or webhook payload, so each one
// is something a third party can choose to send us.
func TestSanitizeRemovesWhatATerminalWouldActOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text is untouched", "Mailbox does not exist", "Mailbox does not exist"},
		{"colour sequence", "\x1b[31mrejected\x1b[0m", "rejected"},
		{"cursor movement", "safe\x1b[2A\x1b[Koverwritten", "safeoverwritten"},
		{
			"osc 8 hyperlink",
			"\x1b]8;;https://evil.example\x07click here\x1b]8;;\x07",
			"click here",
		},
		{"osc 52 clipboard write", "\x1b]52;c;ZXZpbA==\x1b\\reason", "reason"},
		{"bare escape", "a\x1bZb", "ab"},
		{"trailing escape", "reason\x1b", "reason"},
		{"unterminated csi", "reason\x1b[31", "reason"},
		{"unterminated osc", "reason\x1b]8;;http", "reason"},
		{"control characters", "line one\nline two\ttabbed\x00", "line oneline twotabbed"},
		{"delete and c1", "a\x7fb\u009bc", "abc"},
		{"right-to-left override", "moc.elpmaxe\u202E/gro.evil", "moc.elpmaxe/gro.evil"},
		{"zero width", "acme\u200B.com\uFEFF", "acme.com"},
		{"invalid utf-8", "bad\xffbyte", "bad�byte"},
		{"non-ascii text survives", "拒否されました ✓", "拒否されました ✓"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := output.Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A sanitised value must contain nothing a terminal treats as an instruction, whatever the input.
func TestSanitizeLeavesNoEscapeBehind(t *testing.T) {
	t.Parallel()

	hostile := []string{
		"\x1b[1;31m\x1b]8;;file:///etc/passwd\x07x\x1b]8;;\x07\x1b[0m",
		"\x1b]0;retitled\x07\x1b[2J\x1b[H",
		strings.Repeat("\x1b[", 50),
	}

	for _, in := range hostile {
		got := output.Sanitize(in)
		if strings.ContainsAny(got, "\x1b\x07") {
			t.Errorf("Sanitize(%q) left a control byte: %q", in, got)
		}
	}
}

func TestDisplayWidthCountsColumnsNotRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want int
	}{
		{"ascii", "Reminder", 8},
		{"empty", "", 0},
		{"cjk", "予定", 4},
		{"emoji", "🚀", 2},
		{"combining mark", "é", 1},
		{"mixed", "予定 ok", 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := output.DisplayWidth(tc.in); got != tc.want {
				t.Errorf("DisplayWidth(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// A column that overflows breaks every row below it, so the clamp is measured in columns and the
// marker's own width is part of the budget.
func TestClampNeverExceedsTheBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       string
		max      int
		ellipsis string
		want     string
	}{
		{"fits", "Reminder", 10, "…", "Reminder"},
		{"exact fit", "Reminder", 8, "…", "Reminder"},
		{"shortened", "Weekly digest", 8, "…", "Weekly …"},
		{"ascii marker costs three", "Weekly digest", 8, "...", "Weekl..."},
		{"wide characters do not straddle", "予定予定予定", 5, "…", "予定…"},
		{"no room for content", "Weekly digest", 1, "…", "…"},
		{"no room at all", "Weekly digest", 0, "…", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := output.Clamp(tc.in, tc.max, tc.ellipsis)
			if got != tc.want {
				t.Errorf("Clamp(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if w := output.DisplayWidth(got); w > tc.max {
				t.Errorf("Clamp(%q, %d) = %q, which is %d columns wide", tc.in, tc.max, got, w)
			}
		})
	}
}

// Clamping first could cut an escape sequence in half and leave the remainder to be printed.
func TestFieldSanitizesBeforeItClamps(t *testing.T) {
	t.Parallel()

	got := output.Field("\x1b[31mrejected by the remote server\x1b[0m", 12, "…")
	if got != "rejected by…" {
		t.Errorf("Field() = %q", got)
	}
}

func TestParseFormatAcceptsTheDocumentedNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want output.Format
	}{
		{"text", output.Text},
		{"json", output.JSON},
		{"ndjson", output.NDJSON},
		{"yaml", output.YAML},
		{"yml", output.YAML},
		{" JSON ", output.JSON},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := output.ParseFormat(tc.in)
			if err != nil {
				t.Fatalf("ParseFormat(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseFormat(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if got.String() == "unknown" {
				t.Errorf("%v has no name", got)
			}
		})
	}
}

func TestParseFormatRejectsAnythingElse(t *testing.T) {
	t.Parallel()

	_, err := output.ParseFormat("xml")
	if err == nil {
		t.Fatal("an unknown format was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
	}
	if !strings.Contains(err.Error(), "ndjson") {
		t.Errorf("the error does not list the alternatives: %v", err)
	}
}

// The rule that makes the CLI scriptable without a flag: a pipe gets JSON, a terminal gets text.
func TestResolveFollowsTheDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		explicit string
		tty      bool
		want     output.Format
	}{
		{"piped", "", false, output.JSON},
		{"terminal", "", true, output.Text},
		{"forced text into a pipe", "text", false, output.Text},
		{"forced json on a terminal", "json", true, output.JSON},
		{"blank is not a choice", "  ", true, output.Text},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := output.Resolve(tc.explicit, output.Caps{TTY: tc.tty})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("Resolve(%q, tty=%v) = %v, want %v", tc.explicit, tc.tty, got, tc.want)
			}
		})
	}
}

func TestResolveReportsAnUnknownExplicitFormat(t *testing.T) {
	t.Parallel()

	if _, err := output.Resolve("xml", output.Caps{TTY: true}); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}

// result is a view model with a human rendering, standing in for the real ones.
type result struct {
	ID      string `json:"id"`
	To      string `json:"to"`
	Subject string `json:"subject"`
}

func (r result) RenderText(caps output.Caps) []string {
	return []string{
		caps.Glyphs.OK + " Sent",
		"  id  " + r.ID,
		"  to  " + output.Field(r.To, 40, caps.Glyphs.Ellipsis),
	}
}

func TestRenderWritesEachFormatFromOneValue(t *testing.T) {
	t.Parallel()

	value := result{ID: "9f3b2c14", To: "alice@example.com", Subject: "Welcome & hello"}
	caps := output.Caps{Unicode: true, Glyphs: output.UnicodeGlyphs()}

	tests := []struct {
		format output.Format
		want   string
	}{
		{output.Text, "✓ Sent\n  id  9f3b2c14\n  to  alice@example.com\n"},
		{
			output.JSON,
			"{\n  \"id\": \"9f3b2c14\",\n  \"to\": \"alice@example.com\",\n" +
				"  \"subject\": \"Welcome & hello\"\n}\n",
		},
		{
			output.NDJSON,
			`{"id":"9f3b2c14","to":"alice@example.com","subject":"Welcome & hello"}` + "\n",
		},
		{output.YAML, "id: 9f3b2c14\nto: alice@example.com\nsubject: Welcome & hello\n"},
	}

	for _, tc := range tests {
		t.Run(tc.format.String(), func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := output.Render(&buf, tc.format, caps, value); err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("Render(%v) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}
}

// The json tags are the documented field names, so -o yaml must use them too rather than the Go
// field names it would otherwise pick up.
func TestYAMLUsesTheDocumentedFieldNames(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := output.Render(&buf, output.YAML, output.Caps{}, result{ID: "9f3b2c14"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "id: ") {
		t.Errorf("YAML used a Go field name: %q", buf.String())
	}
}

// The two machine formats must describe the same value the same way. Decoding through a Go map
// would sort the keys, so a detail block that reads id, status, subject in JSON would read
// alphabetically in YAML.
func TestYAMLKeepsTheDeclaredFieldOrder(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	value := result{ID: "9f3b2c14", To: "alice@example.com", Subject: "Welcome"}
	if err := output.Render(&buf, output.YAML, output.Caps{}, value); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := "id: 9f3b2c14\nto: alice@example.com\nsubject: Welcome\n"
	if got := buf.String(); got != want {
		t.Errorf("Render(yaml) = %q, want %q", got, want)
	}
}

// A count is an integer and must not acquire a decimal point on its way through JSON.
func TestYAMLCarriesEveryScalarTypeAcross(t *testing.T) {
	t.Parallel()

	type page struct {
		Total     int      `json:"total"`
		Rate      float64  `json:"rate"`
		Truncated bool     `json:"truncated"`
		Cursor    *string  `json:"cursor"`
		IDs       []string `json:"ids"`
	}

	var buf bytes.Buffer
	value := page{Total: 1000000, Rate: 0.5, Truncated: false, IDs: []string{"3a1f9c2d"}}
	if err := output.Render(&buf, output.YAML, output.Caps{}, value); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := "total: 1000000\nrate: 0.5\ntruncated: false\ncursor: null\nids:\n  - 3a1f9c2d\n"
	if got := buf.String(); got != want {
		t.Errorf("Render(yaml) = %q, want %q", got, want)
	}
}

func TestYAMLRendersNestedValues(t *testing.T) {
	t.Parallel()

	type row struct {
		ID   string            `json:"id"`
		Tags map[string]string `json:"tags"`
	}

	var buf bytes.Buffer
	value := []row{{ID: "3a1f9c2d", Tags: map[string]string{"campaign": "launch"}}}
	if err := output.Render(&buf, output.YAML, output.Caps{}, value); err != nil {
		t.Fatalf("Render: %v", err)
	}

	want := "- id: 3a1f9c2d\n  tags:\n    campaign: launch\n"
	if got := buf.String(); got != want {
		t.Errorf("Render(yaml) = %q, want %q", got, want)
	}
}

func TestRenderReportsAValueThatCannotBeEncoded(t *testing.T) {
	t.Parallel()

	// A channel has no JSON form, so every machine format must report rather than panic.
	for _, format := range []output.Format{output.JSON, output.NDJSON, output.YAML} {
		if err := output.Render(&bytes.Buffer{}, format, output.Caps{}, make(chan int)); err == nil {
			t.Errorf("Render(%v) accepted a value with no encoding", format)
		}
	}
}

// NDJSON exists for line-oriented tools, so a listing must arrive as records rather than as one
// array on one line.
func TestNDJSONWritesOneRecordPerLine(t *testing.T) {
	t.Parallel()

	page := []result{{ID: "3a1f9c2d"}, {ID: "7b2e4d81"}}

	var buf bytes.Buffer
	if err := output.Render(&buf, output.NDJSON, output.Caps{}, page); err != nil {
		t.Fatalf("Render: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[1], "7b2e4d81") {
		t.Errorf("second line = %q", lines[1])
	}
}

func TestRenderReportsAValueWithNoTextForm(t *testing.T) {
	t.Parallel()

	err := output.Render(&bytes.Buffer{}, output.Text, output.Caps{}, struct{ A int }{1})
	if err == nil {
		t.Fatal("a value with no text rendering was accepted")
	}
	if got := errs.CodeFor(err); got != errs.CodeInternal {
		t.Errorf("exit code = %d, want %d", got, errs.CodeInternal)
	}
}

func TestRenderRejectsAnUnknownFormat(t *testing.T) {
	t.Parallel()

	if err := output.Render(&bytes.Buffer{}, output.Format(99), output.Caps{}, result{}); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}

// A string result is written raw. `--jq .id` exists to be captured into a shell variable, and a
// quoted value has to be unwrapped before it can be used.
func TestProjectWritesStringsRaw(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := output.Project(&buf, ".id", result{ID: "9f3b2c14", To: "alice@example.com"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got := buf.String(); got != "9f3b2c14\n" {
		t.Errorf("Project() = %q", got)
	}
}

func TestProjectWritesNonStringsAsJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		in   any
		want string
	}{
		{"an object", ".", result{ID: "9f3b2c14"}, `{"id":"9f3b2c14","subject":"","to":""}` + "\n"},
		{"a number", "length", []result{{}, {}}, "2\n"},
		{
			"several results",
			".[] | .id",
			[]result{{ID: "3a1f9c2d"}, {ID: "7b2e4d81"}},
			"3a1f9c2d\n7b2e4d81\n",
		},
		{"no results", ".[] | .id", []result{}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := output.Project(&buf, tc.expr, tc.in); err != nil {
				t.Fatalf("Project: %v", err)
			}
			if got := buf.String(); got != tc.want {
				t.Errorf("Project(%q) = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}

// Both failures are the user's expression, so both are usage errors: reporting either as a server
// problem would send them looking in entirely the wrong place.
func TestProjectReportsBothKindsOfQueryFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
	}{
		{"will not parse", ".["},
		{"fails against the data", ".id + 1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := output.Project(&bytes.Buffer{}, tc.expr, result{ID: "9f3b2c14"})
			if err == nil {
				t.Fatalf("Project(%q) succeeded", tc.expr)
			}
			if got := errs.CodeFor(err); got != errs.CodeUsage {
				t.Errorf("exit code = %d, want %d", got, errs.CodeUsage)
			}
		})
	}
}

func TestConfirmReadsTheAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{"yes", "y\n", true},
		{"spelled out", "yes\n", true},
		{"capitalised", "Y\n", true},
		{"no", "n\n", false},
		{"empty is no", "\n", false},
		{"anything else is no", "maybe\n", false},
		{"a closed stream is no", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var errOut bytes.Buffer
			c := output.NewPrompter(
				strings.NewReader(tc.answer), &errOut, output.Caps{Interactive: true}, false)

			got, err := c.Confirm("Cancel 49 scheduled emails?")
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			if got != tc.want {
				t.Errorf("Confirm() = %v, want %v", got, tc.want)
			}
			// The question goes to the error stream. stdout carries the payload and
			// nothing else, so a caller piping the command gets a document, not a prompt.
			if !strings.Contains(errOut.String(), "(y/N)") {
				t.Errorf("the prompt was not written: %q", errOut.String())
			}
		})
	}
}

func TestConfirmKeepsItsPlaceBetweenQuestions(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	c := output.NewPrompter(strings.NewReader("y\nn\n"), &errOut, output.Caps{Interactive: true}, false)

	first, err := c.Confirm("First?")
	if err != nil {
		t.Fatalf("first Confirm: %v", err)
	}
	second, err := c.Confirm("Second?")
	if err != nil {
		t.Fatalf("second Confirm: %v", err)
	}

	if !first || second {
		t.Errorf("answers = %v, %v; want true, false", first, second)
	}
}

// -y approves the run, so nothing is printed and nothing is read.
func TestConfirmAnswersItselfWithYes(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	c := output.NewPrompter(strings.NewReader(""), &errOut, output.Caps{}, true)

	got, err := c.Confirm("Cancel 49 scheduled emails?")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !got {
		t.Error("Confirm() = false with -y")
	}
	if errOut.Len() != 0 {
		t.Errorf("a prompt was printed with -y: %q", errOut.String())
	}
}

// Neither default is acceptable without a terminal: yes would cancel a batch nobody approved, and
// no would read like the operation was attempted and refused.
func TestConfirmRefusesToGuessWithoutATerminal(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	c := output.NewPrompter(strings.NewReader("y\n"), &errOut, output.Caps{}, false)

	got, err := c.Confirm("Cancel 49 scheduled emails?")
	if err == nil {
		t.Fatal("a question was answered with no terminal to ask on")
	}
	if got {
		t.Error("Confirm() = true on the error path")
	}
	if code := errs.CodeFor(err); code != errs.CodeUsage {
		t.Errorf("exit code = %d, want %d", code, errs.CodeUsage)
	}
	if !strings.Contains(err.Error(), "-y") {
		t.Errorf("the error does not name the flag that answers it: %v", err)
	}
	if errOut.Len() != 0 {
		t.Errorf("a prompt was written to a stream nobody is reading: %q", errOut.String())
	}
}

// A closed pipe is ordinary: `mailkube ... | head -3` closes stdout as soon as it has enough. The
// write has to report rather than panic, so the caller can exit quietly.
func TestWriteLinesReportsABrokenPipe(t *testing.T) {
	t.Parallel()

	err := output.WriteLines(brokenPipe{}, []string{"one", "two"})
	if err == nil {
		t.Fatal("a failed write was ignored")
	}
}

func TestRenderReportsAFailedWrite(t *testing.T) {
	t.Parallel()

	caps := output.Caps{Glyphs: output.ASCIIGlyphs()}
	if err := output.Render(brokenPipe{}, output.Text, caps, result{ID: "9f3b2c14"}); err == nil {
		t.Error("a failed write was ignored")
	}
}

type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
