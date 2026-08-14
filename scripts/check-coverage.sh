#!/usr/bin/env bash
# Fail if total coverage is below the threshold.
#
# Go has no native branch-coverage metric, so this gate is line/statement coverage only
# (see .rules/SOLID_DRY_KISS.md).
set -euo pipefail

THRESHOLD=90

# One well-tested package can carry a repository over a total threshold while another sits at
# nothing, so there is a floor per package as well as a total. Packages smaller than the exemption
# are excluded: a five-statement helper fails an 80% floor at four statements covered, and a gate
# people argue with is a gate people route around.
PACKAGE_FLOOR=80
PACKAGE_EXEMPT_BELOW=30

PROFILE="${1:-coverage.out}"

if [[ ! -f "$PROFILE" ]]; then
  echo "coverage profile '$PROFILE' not found; run 'go test ./... -coverprofile=$PROFILE' first" >&2
  exit 1
fi

total="$(go tool cover -func="$PROFILE" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')"

if [[ -z "$total" ]]; then
  echo "could not parse total coverage from '$PROFILE'" >&2
  exit 1
fi

failed=0

if awk -v t="$total" -v thr="$THRESHOLD" 'BEGIN { exit (t + 0 >= thr) ? 0 : 1 }'; then
  echo "coverage ${total}% meets the ${THRESHOLD}% threshold"
else
  echo "coverage ${total}% is below the required ${THRESHOLD}%" >&2
  failed=1
fi

# The per-package number is computed from the profile's own statement counts rather than from
# `go tool cover -func` averages: a function average weights a one-statement function the same as
# a fifty-statement one, which is not what "80% of this package is covered" means.
#
# Coverage is evaluated per GOOS, over the packages compiled on that GOOS, so a _windows.go file
# is not counted as uncovered on Linux.
per_package="$(awk '
  NR == 1 { next }                        # the mode: line
  {
    location   = $1                       # <import path>/<file>.go:<start>,<end>
    statements = $2
    count      = $3

    # With -coverpkg every test binary reports every block, so the same block appears many
    # times over. Summing the lines directly would multiply each package by the number of test
    # binaries; a block is counted once, and counts as covered if any binary reached it.
    if (!(location in size)) { size[location] = statements }
    if (count + 0 > 0) { reached[location] = 1 }
  }
  END {
    for (location in size) {
      pkg = location
      sub(":.*$", "", pkg)                # drop the line range
      sub("/[^/]*$", "", pkg)             # drop the file name
      total[pkg] += size[location]
      if (location in reached) { hit[pkg] += size[location] }
    }
    for (pkg in total) { printf "%s %d %d\n", pkg, total[pkg], hit[pkg] }
  }
' "$PROFILE" | sort)"

while read -r pkg statements hit; do
  [[ -z "$pkg" ]] && continue
  if (( statements < PACKAGE_EXEMPT_BELOW )); then
    continue
  fi
  pct="$(awk -v h="$hit" -v s="$statements" 'BEGIN { printf "%.1f", (h * 100) / s }')"
  if awk -v p="$pct" -v f="$PACKAGE_FLOOR" 'BEGIN { exit (p + 0 >= f) ? 0 : 1 }'; then
    continue
  fi
  echo "package ${pkg} is at ${pct}%, below the ${PACKAGE_FLOOR}% per-package floor" >&2
  failed=1
done <<< "$per_package"

exit "$failed"
