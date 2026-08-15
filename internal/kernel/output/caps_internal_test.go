package output

import "testing"

// These rules are what stop an automated job hanging on a question nobody can answer, and they
// are unreachable through Detect: an automation environment has no terminal, so the stream check
// short-circuits before any of this runs. Testing them here is the only way they are tested at all.
func TestPromptingIsRefusedWhereNobodyCanAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"a plain shell", map[string]string{"TERM": "xterm-256color"}, true},
		{"nothing set", nil, true},
		{"a dumb terminal", map[string]string{"TERM": "dumb"}, false},
		{"generic ci", map[string]string{"CI": "true"}, false},
		{"github actions", map[string]string{"GITHUB_ACTIONS": "true"}, false},
		{"asked not to", map[string]string{"MAILKUBE_NO_PROMPT": "1"}, false},
		// A variable present but empty is how a shell spells "not set", and treating it as
		// set would silence prompting for anyone who exported CI= once.
		{"empty ci", map[string]string{"CI": ""}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := promptingAllowed(MapEnv(tc.env)); got != tc.want {
				t.Errorf("promptingAllowed(%v) = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
