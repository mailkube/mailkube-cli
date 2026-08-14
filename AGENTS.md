# Project Rules

`mailkube-cli` is the official Mailkube command-line interface: a public (Apache-2.0) Go module
(`github.com/mailkube/mailkube-cli`) producing the `mailkube` binary. Load the relevant rule file
from `.rules/` based on the task.

## Rule Index

> **Index every rule (required).** Every file in `.rules/` MUST have a row in the table below. When you
> add or rename a `.rules/` file, add or update its row in the **same change** — an unindexed rule is
> invisible, because this index is what drives progressive disclosure. The `docs` CI job
> (`scripts/check-rule-index.sh`) fails the build if `.rules/` and this index drift. This convention
> holds for every mailkube repo.

| Rule File | Load When |
|---|---|
| `.rules/ARCHITECTURE.md` | Adding or changing a command, adding a package, or touching `internal/cli` — the feature/kernel layering, the depguard dependency law, the testability seams, the stream contract, and golden files. |
| `.rules/VOICE.md` | Writing anything a user or contributor reads: help text, error messages, README, docs, comments, commit and PR text. |
| `.rules/SOLID_DRY_KISS.md` | Writing or changing any code — the enforced engineering standards (SOLID, DRY, KISS, coverage, docs) and how to run each gate locally. |
| `.rules/RELEASE.md` | Touching `release.yml`, `.releaserc.json`, versioning, or the module's public tags. |

## Key Conventions (always apply)

- **One feature module per capability.** Adding a command is a new directory under
  `internal/features/` plus one line in `internal/cli/registry.go` — never an edit to an existing
  module or a central command list. See `.rules/ARCHITECTURE.md`.
- **The CLI calls the API only through the `mailkube-go` SDK.** No direct REST, no hand-rolled
  request, no URL literal outside `clientfactory`. `depguard` enforces it.
- **stdout is the success payload and nothing else** — progress, warnings, prompts and errors go
  to stderr, and stdout stays empty on failure.
- **Every user-visible screen is a golden file.** Regenerate with `make golden` and read the diff.
- **No version literal anywhere.** The toolchain stamps the module version from the VCS tag and
  `internal/kernel/buildinfo` reads it back, so the reported version equals the released one by
  construction.
- **golangci-lint (v2)** for lint; **gofumpt** for formatting; **staticcheck** (via the standard
  set) is non-negotiable.
- **Doc comment** every exported identifier (`revive` `exported`) and the package
  (`package-comments`).
- **≥ 90% coverage** (line/statement — Go has no branch metric) — enforced by
  `scripts/check-coverage.sh`; never lower the gate to make a change pass.
- **Max cyclomatic 10 / cognitive 20** (`gocyclo`/`gocognit`) — split, don't waive.
- **No duplication** — the `jscpd` gate blocks at > 1% duplicated code; extract shared logic.
- **Conventional Commits** for PR titles (squash-merged); only `feat:`/`fix:`/`perf:` cut a release.
- **`go.work` is never committed** — the committed `go.mod` must resolve the SDK from the proxy or
  `go install` breaks. Never add a `replace` directive.
- **No secrets in the repo** — local config lives in a git-ignored `.env`.
- **Releases are git tags** — `pkg.go.dev` indexes `vX.Y.Z` automatically.
- **Releases commit nothing to `main`** — the git tag is the version and the GitHub Release notes
  are the changelog; there is no `CHANGELOG.md` (see `.rules/RELEASE.md`).
