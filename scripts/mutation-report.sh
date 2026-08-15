#!/usr/bin/env bash
# Report a mutation score for the pure-logic packages.
#
# Go has no branch-coverage metric, so a package can be fully covered by statement count while no
# test asserts anything about a condition inside it. Mutation testing answers the question coverage
# cannot: change the code, and see whether a test notices.
#
# This reports; it does not gate. The run is slow and the tooling is young, and a required check
# with either property is one that gets re-run until it passes and then trusted anyway. The number
# is printed so a drop is visible in the nightly log.
set -euo pipefail

# The efficacy we want to see, and the reason it is not an exit code. Raise it as the number
# settles rather than pinning it to whatever today's run produced.
TARGET=75

# Pure-logic packages only. A mutation inside an IO, network or process seam usually produces a
# mutant that no test *should* kill, and a score dominated by those cannot be acted on.
PACKAGES=(
  ./internal/kernel/errs
  ./internal/kernel/input
  ./internal/kernel/output
  ./internal/kernel/routes
  ./internal/kernel/settings
  ./internal/kernel/smtp
  ./internal/features/emails
  ./internal/features/scheduled
  ./internal/features/meta/errors
)

if ! command -v gremlins >/dev/null 2>&1; then
  echo "gremlins is not installed: go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.5.0" >&2
  exit 1
fi

status=0
for pkg in "${PACKAGES[@]}"; do
  echo "=== ${pkg}"
  # --dry-run is deliberately absent: a dry run counts mutants without executing the tests, which
  # is a count of opportunities rather than a score.
  if ! gremlins unleash --tags="" "${pkg}"; then
    echo "gremlins failed on ${pkg}" >&2
    status=1
  fi
done

echo
echo "Target efficacy is ${TARGET}%. This job reports; it never blocks a merge."
exit "${status}"
