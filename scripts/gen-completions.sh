#!/usr/bin/env bash
# Generate the shell completion files that ship inside the release archives and packages.
#
# They are generated from the command tree rather than written by hand, so a command or flag
# added without a matching completion is not a thing that can happen.
#
# Output goes to ./completions/, which git ignores. That location is not incidental: the
# toolchain reads the version out of the VCS state, so a generated file anywhere tracked would
# mark the checkout dirty and produce a binary reporting a version the release does not have. It
# cannot go under dist/ either, because the release build requires that directory to be empty
# when it starts.
set -euo pipefail

out="completions"
mkdir -p "$out"

# Build once and run it four times. `go run` per shell would compile the whole program four times
# for four short strings.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

go build -trimpath -o "$tmp/mailkube" ./cmd/mailkube

"$tmp/mailkube" completion bash >"$out/mailkube.bash"
"$tmp/mailkube" completion zsh >"$out/mailkube.zsh"
"$tmp/mailkube" completion fish >"$out/mailkube.fish"
"$tmp/mailkube" completion powershell >"$out/mailkube.ps1"

echo "Completions written to $out/"
