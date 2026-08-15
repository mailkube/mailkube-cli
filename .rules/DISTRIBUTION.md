# Distribution & Release Channels

Load this when touching `.goreleaser.yaml`, `install.sh`, `install.ps1`, the `Dockerfile`, or any
workflow under `.github/workflows/` that publishes or verifies a release.

`.rules/RELEASE.md` covers how a version is decided. This covers what happens once one exists.

## The channels

| Channel | Artifact | Updates when |
|---|---|---|
| GitHub Releases | archives, packages, checksums, signature, SBOMs | the release is published; this is the source every other channel reads |
| `install.sh` / `install.ps1` | the archive for the current platform | you run it; it always resolves the latest release unless `MAILKUBE_VERSION` says otherwise |
| Homebrew | a cask in the tap repository | the tap job pushes, immediately after the release |
| Scoop | a manifest in the bucket repository | the bucket job pushes, immediately after the release |
| `go install` | built from the tagged source | the module proxy has fetched the tag, which can lag the release by minutes |
| Container image | a distroless image, two architectures | the release job pushes it |
| `.deb` / `.rpm` / `.apk` | release assets | never automatically: they are files to download, not a repository to subscribe to |

**Nothing auto-updates, and nothing checks for updates.** `version --check` asks, and only when you
run it. A tool that contacts a server you did not ask it to contact is a tool that cannot be run in
an air-gapped build, and it is not the deal this one offers.

## The version has one source, and the release asserts it

The toolchain stamps the module version from the VCS tag; `internal/kernel/buildinfo` reads it back
out of the binary. There is no linker flag, no generated constant and no literal, which is what
`scripts/check-no-version-literal.sh` keeps true.

**It only works from a clean checkout.** A build from a tree with modified tracked files is stamped
as a dirty build, and the binary then reports a version that was never released. Two things guard
that, and neither is optional:

- generated files land in ignored directories, never in the tracked tree. `scripts/gen-completions.sh`
  writes to `completions/`, which is ignored, and cannot write under `dist/` because the release
  build requires that directory to be empty when it starts;
- the publish job generates the completions, asserts the tree is still clean, builds, and compares
  what the binary reports against the tag it is releasing. That happens **before** anything is
  published.

If you add a step that writes into the tree, that assertion is what will tell you.

## The release is drafted, then filled, then published — in that order

**A published release cannot be added to.** GitHub seals one on publication: its assets and its tag
are fixed from that moment and an upload afterwards is refused, so a release that is published
before its artifacts exist is a release that can never carry them. That is permanent per tag; the
only remedy is a new version.

So the order is fixed, and three settings hold it:

| Step | Who | Setting |
|---|---|---|
| Draft the release, with the notes | `.releaserc.json` | `draftRelease: true` |
| Fill it with every artifact | `.goreleaser.yaml` | `use_existing_draft: true` |
| Publish it, last | `.goreleaser.yaml` | the absence of `draft: true` |

**The draft is matched by name, not by tag.** The name the notes are drafted under and
`release.name_template` have to be the same string, or the draft is passed over in silence and the
uploads fail. Both spell it `v1.2.3`; move one and you move both.

This is also why a failed publish leaves nothing visible. The release stays a draft until the last
step, so a run that dies part-way leaves a draft rather than a public, empty release.

## Publishing is per channel

`publish.yml` has one job per channel so a channel that fails can be retried alone. Without that, a
failure after the release is made leaves some channels on the new version and others silently on
the old one, and nobody finds it until the *next* release, because the first was fixed by hand.

- **Recovering a partial publish**: re-run the failed job. The channels that succeeded are
  untouched.
- **The tap and the bucket** are idempotent without qualification: each re-run overwrites the
  manifest it wrote.
- **`assets` is idempotent only while the release is a draft**, which is the whole window in which
  it is failing, and so the whole window in which a re-run is what you want. After it succeeds the
  release is sealed, a re-run is not the recovery path, and re-releasing means a new version.
- **Do not** make a publish job depend on state built by a previous run of the workflow. Each job
  must be able to reach its end state from the tag alone, or from an artifact of the run it is in.

The tap and bucket jobs write to **other repositories**, so they use a separate credential
(`TAP_GITHUB_TOKEN`). The token this workflow gets by default cannot write outside this repository,
and discovering that during a release blocks two channels at once.

## Prerequisites that must exist before the first release

These are provisioning, not code, and none of them can be created by the pipeline that needs them:

- the `mailkube/homebrew-tap` repository;
- the `mailkube/scoop-bucket` repository;
- a fine-grained token with content write access to **those two repositories only**, stored as
  `TAP_GITHUB_TOKEN`;
- the `release` environment, with whatever protection rules the repository wants on it.

## Verification

`verify-release.yml` installs what was published, the way a user would, and asserts every channel
yields the released version. It reads nothing from the build; every job starts from the published
artifacts, because the failure it exists to catch is a release that built correctly and published
wrong.

Verification is executed, never described. The install scripts are **run**, not only linted:
linting proves the syntax, running proves the download, the checksum comparison, the signature
check, the extraction and the resulting binary.

## Trust

- **Checksums are mandatory in both installers, and there is no switch that skips them.** A script
  that runs with permission to write to a directory on your PATH has no business extracting bytes
  it has not checked, and a bypass flag becomes the answer to "it does not work on my machine".
- **Download, verify, then extract.** Never extract and check afterwards: by then the bytes are
  already on disk.
- **The signature covers the checksum file**, which covers every artifact, so one verification
  covers the whole release. It ships as a single Sigstore bundle, `checksums.txt.sigstore.json`,
  holding the signature, the certificate and the transparency-log entry together. cosign is
  optional to install and used when present.
- **cosign is pinned to a major version in the workflows**, because the signing and verification
  flags belong to it rather than to us. The version that signs and the version a user verifies
  with have to agree on a format, so moving it is a deliberate change to this file and both
  installers, never a version float.
- **The installers pin the signing identity to `publish.yml` on the default branch**, because that
  is the run that signs: the release is cut by a push to that branch and the tag is created during
  the run, so the certificate names the branch and never a tag. Anchored at both ends, since the
  publish workflow can also be started by hand from any branch. Moving the release trigger means
  moving this identity in both installers, in the same change.
- **A failed signature prints cosign's own reason before the verdict.** Without it every failure
  reads the same, and the difference between a stale bundle, an identity that moved and a tampered
  file is the whole diagnosis.
- **No `--insecure`, anywhere.** Not in the installers, not on either transport.

## Deliberately not done

Each of these is a decision, not an omission. Reopen them with a reason, not a preference.

- **No hosted package repository.** `.deb`/`.rpm`/`.apk` are release assets. Hosting a repository
  means signing keys, an origin and a cache to keep correct, for a single binary with no
  dependencies.
- **No vanity install host.** The installers are served from the release itself, so the script a
  user runs and the release it installs are the same artifact and there is no second place to keep
  current.
- **No man page.** The only maintained way to generate one adds a markdown renderer to the
  dependency list for a page that `--help`, `topic` and the documentation site already cover.
- **No auto-update, and no update check unless asked.** See above.
- **No code signing for macOS or Windows yet.** The toolchain ad-hoc signs the macOS binary well
  enough to run, and the install script clears the download marker; a browser-downloaded archive
  shows a prompt, which the installation page states.
- **macOS is Apple Silicon only.** An Intel Mac builds from source with `go install`, which the
  install script says when it is run on one. Shipping the second architecture would mean shipping
  a binary the toolchain does not sign, so the signing story would have to become a sentence with
  a condition in it rather than a fact.
