package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestATaggedBuildReportsItsModuleAndSDKVersions(t *testing.T) {
	got := fromBuildInfo(&debug.BuildInfo{
		GoVersion: "go1.24.0",
		Main:      debug.Module{Version: "v1.2.3"},
		Deps: []*debug.Module{
			{Path: "github.com/spf13/cobra", Version: "v1.10.2"},
			{Path: sdkModulePath, Version: "v1.1.0"},
		},
	})

	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q", got.Version)
	}
	if got.SDKVersion != "v1.1.0" {
		t.Errorf("SDKVersion = %q", got.SDKVersion)
	}
	if got.GoVersion != "go1.24.0" {
		t.Errorf("GoVersion = %q", got.GoVersion)
	}
}

func TestAWorkingTreeBuildReportsDevRatherThanDevel(t *testing.T) {
	// The toolchain writes "(devel)" for an untagged build. Surfacing that verbatim would put
	// a Go implementation detail in front of a user.
	got := fromBuildInfo(&debug.BuildInfo{GoVersion: "go1.24.0", Main: debug.Module{Version: "(devel)"}})

	if got.Version != "dev" {
		t.Errorf("Version = %q, want dev", got.Version)
	}
}

func TestAnAbsentSDKDependencyIsReportedAsUnknown(t *testing.T) {
	got := fromBuildInfo(&debug.BuildInfo{GoVersion: "go1.24.0", Main: debug.Module{Version: "v1.0.0"}})

	if got.SDKVersion != "unknown" {
		t.Errorf("SDKVersion = %q, want unknown", got.SDKVersion)
	}
}

func TestUnknownIsFullyPopulated(t *testing.T) {
	got := unknown()
	if got.Version == "" || got.SDKVersion == "" || got.GoVersion == "" {
		t.Errorf("unknown() has an empty field: %+v", got)
	}
}
