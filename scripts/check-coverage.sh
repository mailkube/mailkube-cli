#!/usr/bin/env bash
# Fail if total coverage is below the threshold.
#
# Go has no native branch-coverage metric, so this gate is line/statement coverage only
# (see .rules/SOLID_DRY_KISS.md).
set -euo pipefail

THRESHOLD=90
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

if awk -v t="$total" -v thr="$THRESHOLD" 'BEGIN { exit (t + 0 >= thr) ? 0 : 1 }'; then
  echo "coverage ${total}% meets the ${THRESHOLD}% threshold"
else
  echo "coverage ${total}% is below the required ${THRESHOLD}%" >&2
  exit 1
fi
