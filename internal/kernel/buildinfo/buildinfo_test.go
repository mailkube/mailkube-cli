package buildinfo_test

import (
	"testing"

	"github.com/mailkube/mailkube-cli/internal/kernel/buildinfo"
)

func TestReadNeverReturnsEmptyFields(t *testing.T) {
	// A test binary is built from a working tree, so there is no module version to read. The
	// point of this test is that the no-version case still answers something true rather than
	// an empty string, which would render as a blank in `mailkube version`.
	got := buildinfo.Read()

	if got.Version == "" {
		t.Error("Version is empty; an untagged build must report dev")
	}
	if got.SDKVersion == "" {
		t.Error("SDKVersion is empty; an unknown dependency must report unknown")
	}
	if got.GoVersion == "" {
		t.Error("GoVersion is empty")
	}
}

func TestAnUntaggedBuildReportsDevRatherThanAZeroVersion(t *testing.T) {
	// "0.0.0" reads like a release that went out; "dev" is a true statement about the build.
	if got := buildinfo.Read().Version; got == "0.0.0" || got == "(devel)" {
		t.Errorf("Version = %q, want a human-meaningful value", got)
	}
}
