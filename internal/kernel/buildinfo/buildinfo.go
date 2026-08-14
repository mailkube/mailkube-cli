// Package buildinfo reports the versions of the binary and of the SDK it was built against.
//
// There is deliberately no version literal anywhere in this repository. The Go toolchain stamps
// the module version into the build info from the VCS tag, and this package reads it back, so
// the version the binary reports equals the version that was released, by construction. A
// hand-maintained constant is how a tool ends up confidently reporting a version it is not.
package buildinfo

import "runtime/debug"

// sdkModulePath is the SDK whose version `mailkube version` reports alongside its own.
const sdkModulePath = "github.com/mailkube/mailkube-go"

// Info is the set of versions the CLI can report about itself.
type Info struct {
	// Version is the CLI's own module version, or "dev" when built outside a tagged tree.
	Version string
	// SDKVersion is the version of the Mailkube SDK this binary was built against.
	SDKVersion string
	// GoVersion is the toolchain that built the binary.
	GoVersion string
}

// Read returns the build information recorded in this binary.
//
// Reading rather than stamping is what keeps the reported version honest, but it does mean a
// binary built from a working tree — `go run`, `go build` without a tag — has no version to
// report. That case answers "dev" rather than an empty string or a zero version, because "dev"
// is a true statement about the build and "0.0.0" looks like a release.
func Read() Info {
	raw, ok := debug.ReadBuildInfo()
	if !ok {
		return unknown()
	}
	return fromBuildInfo(raw)
}

// unknown is the answer when the runtime has no build info to give, which happens in a binary
// built without module support.
func unknown() Info {
	return Info{Version: "dev", SDKVersion: "unknown", GoVersion: "unknown"}
}

// fromBuildInfo extracts the versions from build info.
//
// Separated from Read so it can be tested against build info a test constructs. Read itself can
// only ever observe the one build it is running inside, which means the interesting cases — a
// tagged release, a missing SDK dependency — are unreachable from a test binary.
func fromBuildInfo(raw *debug.BuildInfo) Info {
	info := unknown()
	info.GoVersion = raw.GoVersion

	if v := raw.Main.Version; v != "" && v != "(devel)" {
		info.Version = v
	}
	for _, dep := range raw.Deps {
		if dep.Path == sdkModulePath {
			info.SDKVersion = dep.Version
			break
		}
	}
	return info
}
