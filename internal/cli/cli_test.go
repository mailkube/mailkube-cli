package cli_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

func TestEveryRegisteredFeatureContributesACommand(t *testing.T) {
	// The registry is the entire wiring, so the property worth asserting is that registering a
	// feature is sufficient: nothing else has to be edited for its command to appear.
	streams, _, _ := feature.TestStreams()
	root := cli.NewRootCmd(&feature.Deps{IO: streams})

	registered := map[string]bool{}
	for _, cmd := range root.Commands() {
		registered[cmd.Name()] = true
	}

	for _, f := range cli.Registry() {
		if !registered[f.Name()] {
			t.Errorf("feature %q is in the registry but contributes no command of that name", f.Name())
		}
	}
}

func TestTheRegistryHasNoDuplicateNames(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range cli.Registry() {
		if seen[f.Name()] {
			t.Errorf("feature %q is registered twice", f.Name())
		}
		seen[f.Name()] = true
	}
}

func TestEachCallBuildsAFreshCommandTree(t *testing.T) {
	// Cobra commands hold parsed flag state. A shared tree leaks one invocation's flags into
	// the next — invisible in production, where the process runs once, and maddening in tests.
	streams, _, _ := feature.TestStreams()
	first := cli.NewRootCmd(&feature.Deps{IO: streams})
	second := cli.NewRootCmd(&feature.Deps{IO: streams})

	if first == second {
		t.Error("NewRootCmd returned the same command instance twice")
	}
}

func TestTheRootCommandDescribesItself(t *testing.T) {
	streams, out, _ := feature.TestStreams()
	root := cli.NewRootCmd(&feature.Deps{IO: streams})
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out.String(), "mailkube") {
		t.Errorf("help output does not name the binary:\n%s", out.String())
	}
}
