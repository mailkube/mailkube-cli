package testsupport

import "strings"

// GoldenConfigPath is the config path every golden file records.
//
// A run that writes its config needs a real file, which means a temporary directory that differs on
// every run and on every machine. The path is genuinely part of what several screens print, so
// rather than keep it out of the output, a golden test rewrites it to this one before comparing.
const GoldenConfigPath = "/tmp/mailkube-golden/config.toml"

// StablePath rewrites a run's real config path to GoldenConfigPath.
//
// A run that already used GoldenConfigPath comes back unchanged.
func StablePath(s, actual string) string {
	return strings.ReplaceAll(s, actual, GoldenConfigPath)
}
