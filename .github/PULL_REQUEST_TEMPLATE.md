<!--
PR titles MUST follow Conventional Commits (e.g. `fix(client): ...`) — it is CI-enforced and
becomes the squash-merge commit message. Only feat/fix/perf trigger a release.
-->

## What

<!-- Describe the change in 1–2 sentences. -->

## Why

<!-- The user-visible problem this solves, or the motivation. -->

## Quality checklist

- [ ] `golangci-lint run` passes (complexity, docs, SOLID smells, staticcheck)
- [ ] `gofumpt -l .` is clean (formatting)
- [ ] `go vet ./...` passes
- [ ] `go test ./... -race -coverprofile=coverage.out` passes and `./scripts/check-coverage.sh` reports ≥ 90%
- [ ] `npx jscpd --config .jscpd.json .` clean (no new duplication)
- [ ] Docs updated (`README.md`) if user-visible

## Engineering standards (SOLID / DRY / KISS)

- [ ] Single-responsibility: new/changed units do one thing; no god-functions
- [ ] No duplication introduced; shared logic extracted (DRY)
- [ ] Public APIs documented (doc comments — revive passes)
- [ ] Complexity within limit (no `//nolint` complexity waivers added)
- [ ] Depends on abstractions/interfaces where a boundary is crossed (DIP)

## Notes

<!-- Optional: screenshots, follow-ups, breaking-change details. -->
