package errors_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mailkube/mailkube-cli/internal/features/meta/errors"
)

// sdkModule is the module whose error names must all be explained.
const sdkModule = "github.com/mailkube/mailkube-go"

// TestEveryPublishedErrorNameIsExplained is the parity half of the surface gate.
//
// The SDK declares one constant per documented error name, and this reads those constants out of
// its source rather than out of a list copied into this repository. That is the whole point: a
// copied list is a list that drifts, and the failure mode of drift here is a user meeting an
// error this command has nothing to say about, which is exactly when they need it most.
//
// The set stays open at runtime — an unknown name still renders, with the server's own message —
// but it may not be open at build time, because every name upstream documents is one we could
// have explained and did not.
func TestEveryPublishedErrorNameIsExplained(t *testing.T) {
	t.Parallel()

	published := publishedNames(t)
	if len(published) == 0 {
		t.Fatal("no error-name constants were found in the SDK; the parity check is not checking anything")
	}

	explained := make(map[string]bool, len(errors.APINames()))
	for _, name := range errors.APINames() {
		explained[name] = true
	}

	for _, name := range published {
		if !explained[name] {
			t.Errorf("the SDK documents %q and `errors explain` has no entry for it", name)
		}
	}

	// The other direction matters too, though it fails differently: an entry for a name the
	// platform does not use is dead text nobody will ever be shown, and usually a typo.
	documented := make(map[string]bool, len(published))
	for _, name := range published {
		documented[name] = true
	}
	for _, name := range errors.APINames() {
		if !documented[name] {
			t.Errorf("`errors explain` carries %q, which the SDK does not declare", name)
		}
	}
}

// publishedNames reads the values of the SDK's ErrorName* constants.
func publishedNames(t *testing.T) []string {
	t.Helper()

	source := filepath.Join(moduleDir(t), "errors.go")
	file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", source, err)
	}

	var names []string
	for _, decl := range file.Decls {
		general, ok := decl.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		names = append(names, constValues(t, general)...)
	}
	return names
}

// constValues collects the string values of the ErrorName* constants in one declaration.
func constValues(t *testing.T, decl *ast.GenDecl) []string {
	t.Helper()

	var values []string
	for _, spec := range decl.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
			continue
		}
		if !strings.HasPrefix(value.Names[0].Name, "ErrorName") {
			continue
		}

		literal, ok := value.Values[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			continue
		}
		unquoted, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatalf("reading %s: %v", value.Names[0].Name, err)
		}
		values = append(values, unquoted)
	}
	return values
}

// moduleDir locates the SDK's source, wherever the build resolved it from.
//
// Asking the toolchain rather than assuming a path is what lets this work identically against the
// module cache in CI and against a local checkout in a workspace, which are the two ways this
// repository is ever built.
func moduleDir(t *testing.T) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", sdkModule).Output()
	if err != nil {
		t.Fatalf("locating %s: %v", sdkModule, err)
	}

	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("the toolchain reported no directory for %s", sdkModule)
	}
	return dir
}
