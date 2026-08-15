package cli_test

import (
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/cli"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/testsupport"
)

// TestSurfaceMappingsAreWellFormedAndUnique is the registry half of the surface gate.
//
// A feature that reaches the API declares which operations it covers, and `doctor` and the parity
// checks walk the registry rather than a hand-maintained list. That only works if the declarations
// are trustworthy, so this is what makes them so: each one names a method and a path, and no two
// features claim the same operation.
//
// Two features claiming one operation is the interesting failure. It means either a capability was
// implemented twice or a mapping was copied and not edited, and both produce a parity report that
// looks complete while covering less than it says.
func TestSurfaceMappingsAreWellFormedAndUnique(t *testing.T) {
	t.Parallel()

	methods := map[string]bool{
		"GET": true, "POST": true, "PATCH": true, "PUT": true, "DELETE": true,
	}
	claimed := map[string]string{}

	for _, f := range cli.Registry() {
		documented, ok := f.(feature.Documented)
		if !ok {
			continue
		}

		for _, operation := range documented.SurfaceMapping() {
			method, path, found := strings.Cut(operation, " ")
			if !found || !methods[method] {
				t.Errorf("%s declares %q, which does not begin with an HTTP method",
					f.Name(), operation)
				continue
			}
			if !strings.HasPrefix(path, "/") {
				t.Errorf("%s declares %q, whose path is not absolute", f.Name(), operation)
				continue
			}
			if owner, taken := claimed[operation]; taken {
				t.Errorf("%s and %s both claim %q", owner, f.Name(), operation)
				continue
			}
			claimed[operation] = f.Name()
		}
	}

	if len(claimed) == 0 {
		t.Error("no feature declares a surface mapping, so the parity gate checks nothing")
	}
}

// TestEveryFeatureIsReachableFromTheRoot guards the wiring the registry promises.
//
// Adding a feature is meant to be one directory and one line here, with nothing else to remember.
// This is what makes that true rather than merely intended: a feature in the registry that
// contributes no reachable command is a capability that exists in the code and nowhere else.
func TestEveryFeatureIsReachableFromTheRoot(t *testing.T) {
	t.Parallel()

	got := run(t, testsupport.TestOptions{}, "commands", "-o", "text")
	if got.code != 0 {
		t.Fatalf("listing the command tree: %s", got.errOut)
	}

	for _, f := range cli.Registry() {
		// The feature's name is not always its command name, so the check is that its
		// primary command appears, which is what the tree is built from.
		if !strings.Contains(got.out, f.Name()) {
			t.Errorf("feature %q contributes nothing reachable from the root", f.Name())
		}
	}
}
