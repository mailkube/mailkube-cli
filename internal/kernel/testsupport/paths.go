package testsupport

import (
	"encoding/json"
	"strings"
)

// GoldenConfigPath is the config path every golden file records.
//
// A run that writes its config needs a real file, which means a temporary directory that differs on
// every run and on every machine. The path is genuinely part of what several screens print, so
// rather than keep it out of the output, a golden test rewrites it to this one before comparing.
const GoldenConfigPath = "/tmp/mailkube-golden/config.toml"

// StablePath rewrites a run's real config path to GoldenConfigPath.
//
// It rewrites both spellings a screen can print the path in: as is, on a text screen, and escaped,
// inside a JSON document. The two differ only where the path holds a character JSON escapes, which
// on Windows is every separator, so a helper that knew only the first would pass everywhere the
// golden was written and fail on Windows alone. The escaped form goes first because it is the
// longer of the two. A run that already used GoldenConfigPath comes back unchanged.
func StablePath(s, actual string) string {
	s = strings.ReplaceAll(s, jsonEscaped(actual), GoldenConfigPath)
	return strings.ReplaceAll(s, actual, GoldenConfigPath)
}

// jsonEscaped spells a string the way the JSON renderer does between its quotes.
//
// It uses the renderer's own encoder settings (see output.Render) rather than a hand-written
// escape, so the two cannot drift apart.
func jsonEscaped(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	// Encoding a string cannot fail: every string has a JSON spelling.
	_ = enc.Encode(s)
	quoted := strings.TrimSuffix(b.String(), "\n")
	return strings.TrimSuffix(strings.TrimPrefix(quoted, `"`), `"`)
}
