#!/usr/bin/env bash
# Fail if a version number has been written into the source.
#
# The version has exactly one source: the toolchain stamps the module version from the VCS tag,
# and internal/kernel/buildinfo reads it back out of the binary's own build information. A literal
# anywhere else is a second source, and two sources drift silently, because the binary keeps
# reporting whichever one someone remembered to bump. The failure that follows is a user quoting
# to support a version that was never released.
#
# Two shapes are refused, and the pair is deliberately narrow. Anything broader flags an IP
# address or a timeout and teaches people to route around the rule rather than meet it:
#
#   1. something *named* like a version assigned a numeric string, as in `Version = "1.2.3"`;
#   2. a `v`-prefixed semver literal anywhere in code, which has no other plausible meaning.
#
# Test files and testdata are exempt. Fixtures say versions out loud by their nature, and a golden
# file that could not name one would not be testing the screen a user sees.
set -euo pipefail

named='[Vv]ersion[A-Za-z0-9_]*[[:space:]]*:?=[[:space:]]*"v?[0-9]+\.[0-9]+'
prefixed='"v[0-9]+\.[0-9]+\.[0-9]+"'

report() {
  echo "ERROR: $1 declares a version literal:" >&2
  while IFS= read -r line; do
    echo "  $line" >&2
  done <<<"$2"
}

found=0
while IFS= read -r file; do
  case "$file" in
  *_test.go | */testdata/*) continue ;;
  esac

  # Comment lines are excluded: a version named in prose is documentation, not a second source.
  code=$(grep -vE '^[[:space:]]*//' "$file" || true)

  if matches=$(printf '%s\n' "$code" | grep -nE "$named|$prefixed"); then
    report "$file" "$matches"
    found=1
  fi
done < <(find . -name '*.go' -not -path './dist/*' -not -path './vendor/*')

if [[ "$found" -ne 0 ]]; then
  cat >&2 <<'EOF'

The version comes from the VCS tag and is read back through internal/kernel/buildinfo.
Remove the literal and use that instead. See .rules/RELEASE.md.
EOF
  exit 1
fi

echo "Version-literal check OK: the tag is the only source of the version."
