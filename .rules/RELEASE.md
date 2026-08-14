# Release & Publishing

Load this when touching `release.yml`, `.releaserc.json`, versioning, or the module's public tags.

## The contract

1. **Conventional Commits drive the version.** On push to `main`, `semantic-release` reads the commit
   history since the last tag: `fix:` → patch, `feat:` → minor, `feat!:`/`BREAKING CHANGE:` → major.
   `perf:` also releases. Anything else (`chore`, `docs`, `ci`, `refactor`, `test`) does **not** release.
2. **It creates the tag `vX.Y.Z` and the GitHub Release, and writes nothing else.** No commit, no
   `CHANGELOG.md`, no version bump in the tree. The `v`-prefixed semver tag is what Go module
   consumers and `pkg.go.dev` resolve. See "Why nothing is committed back to `main`".
3. **The tag IS the version, with no wiring at all.** Go bakes the module version of the tag it
   was built from into the binary, and `Version()` reads it back out of `debug.ReadBuildInfo()` for
   the `User-Agent`. There is no version literal anywhere in this repo and none is needed.
4. **There is no publish job.** Go has no registry upload step — `pkg.go.dev` indexes the module
   automatically the first time someone requests `github.com/mailkube/mailkube-cli@vX.Y.Z`. No token,
   no OIDC, no environment secret is involved.

## Why nothing is committed back to `main`

`main` is covered by a ruleset requiring a pull request and the gated checks. A `chore(release):`
commit pushed straight to `main` by the workflow violates it, and the obvious fix does not exist:
**`github-actions[bot]` cannot be added to a ruleset bypass list.** Bypass is available to admins,
the maintain/write role, teams, GitHub Apps and Dependabot, and the built-in Actions identity is none
of those. Making the commit work would mean introducing a separate identity — a GitHub App or a
deploy key — purely to write a version number that the tag already carries.

So `.releaserc.json` loads neither `@semantic-release/git` nor `@semantic-release/changelog`. The
release writes one tag and one GitHub Release. **The generated release notes are the changelog**;
there is no `CHANGELOG.md` in this repo, and adding one back would reintroduce the commit.

## Required GitHub setup (one-time, per repo)

- GitHub **environment** `release` should exist (Settings → Environments) with protection rules; the
  `release` job runs in it. No `pypi`/registry environment is needed.
- Nothing to register on any package registry. To surface a new tag on `pkg.go.dev` immediately, you may
  fetch it once: `GOPROXY=proxy.golang.org go list -m github.com/mailkube/mailkube-cli@vX.Y.Z`.

## Do not

- Do not add a `CHANGELOG.md`, a `@semantic-release/git` plugin, or a version literal in Go
  source, and do not move tags. The tag is the only source of truth.
- Do not add a registry token or publish step — Go modules are consumed straight from the tagged source.
- Do not gate `release.yml` on anything weaker than the full `ci.yml` (`test` + `dry` + `docs`).
