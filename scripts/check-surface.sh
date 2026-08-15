#!/usr/bin/env bash
# Fail if the CLI's coverage of the published surface has drifted.
#
# Two things are checked, and both are checks against a source of truth outside this repository's
# own opinion of itself:
#
#   1. every error name the SDK declares has an entry in `errors explain`, read out of the SDK's
#      source rather than out of a list copied here;
#   2. every feature's declared operations are well-formed and unclaimed by any other feature, so
#      the parity report cannot look complete while covering less than it says.
#
# It runs the same tests `make test` runs. That is deliberate: a gate with its own second
# implementation is a gate that can pass while the suite fails. Naming them here is what makes a
# drift failure attributable at a glance, instead of one line inside a full test run.
set -euo pipefail

TESTS='TestEveryPublishedErrorNameIsExplained|TestSurfaceMappingsAreWellFormedAndUnique|TestEveryFeatureIsReachableFromTheRoot'

echo "Checking surface parity…"
go test ./... -run "$TESTS" -count=1

echo "Surface parity OK."
