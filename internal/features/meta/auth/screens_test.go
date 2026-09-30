package auth_test

import (
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// TestScreens pins the screens `auth login` renders once a key has been verified.
//
// These live here rather than with the other screens because a verified login needs a probe
// answer, and the composition root deliberately offers no way to substitute the client that
// gives one. The text screen reports the verdict and nothing else; the JSON document also carries
// the server's answer, exactly as it arrived, for a caller that wants it.
func TestScreens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
	}{
		{name: "login", format: "text"},
		{name: "login_json", format: "json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps, out, errOut := testsupport.TestDeps(t, testsupport.TestOptions{
				Globals: &settings.Globals{Output: tc.format},
			})
			if err := runCommand(t, featureWith(acceptedKey()), deps, "login", "--key", "mk_j3k1a2b3c4d5f8a2"); err != nil {
				t.Fatalf("login: %v", err)
			}
			assertScreen(t, tc.name, deps, out, errOut)
		})
	}
}
