package testsupport_test

import (
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// TestStablePathRewritesEveryFormThePathIsPrintedIn covers the two spellings a screen prints a
// path in. A text screen prints it as is; a JSON screen escapes it, and on Windows every separator
// in a temporary directory is a backslash, which JSON doubles. Rewriting only the first form lets
// the real temporary directory through into every JSON golden that prints a path, on Windows
// alone, so the failure never shows on the machine the golden was written on.
func TestStablePathRewritesEveryFormThePathIsPrintedIn(t *testing.T) {
	t.Parallel()

	const windows = `C:\Users\RUNNER~1\AppData\Local\Temp\TestScreens001\mailkube\config.toml`
	const unix = "/var/folders/xy/T/TestScreens001/mailkube/config.toml"

	tests := []struct {
		name   string
		actual string
		in     string
		want   string
	}{
		{
			name:   "text, unix",
			actual: unix,
			in:     "Saved to " + unix + "  (mode 0600)",
			want:   "Saved to " + testsupport.GoldenConfigPath + "  (mode 0600)",
		},
		{
			name:   "text, windows",
			actual: windows,
			in:     "Saved to " + windows + "  (mode 0600)",
			want:   "Saved to " + testsupport.GoldenConfigPath + "  (mode 0600)",
		},
		{
			name:   "json, windows",
			actual: windows,
			in:     `{"path": "C:\\Users\\RUNNER~1\\AppData\\Local\\Temp\\TestScreens001\\mailkube\\config.toml"}`,
			want:   `{"path": "` + testsupport.GoldenConfigPath + `"}`,
		},
		{
			name:   "already stable",
			actual: testsupport.GoldenConfigPath,
			in:     "Saved to " + testsupport.GoldenConfigPath,
			want:   "Saved to " + testsupport.GoldenConfigPath,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := testsupport.StablePath(tc.in, tc.actual); got != tc.want {
				t.Errorf("StablePath(%q, %q)\n got: %s\nwant: %s", tc.in, tc.actual, got, tc.want)
			}
		})
	}
}
