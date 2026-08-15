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

## Publishing is per channel, and every job is idempotent

`publish.yml` has one job per channel, and re-running a job against a tag that is already published
replaces what that job produced rather than failing on a duplicate.

That is the whole reason for the split. A channel that fails after the release is made would
otherwise leave some channels on the new version and others silently on the old one, and nobody
finds it until the *next* release, because the first was fixed by hand.

- **Recovering a partial publish**: re-run the failed job. The channels that succeeded are
  untouched.
- **Publishing an existing tag from scratch**: run `publish.yml` manually with the tag.
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
  covers the whole release. cosign is optional to install and used when present.
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
- **No code signing for macOS or Windows yet.** The toolchain signs the macOS binaries well enough
  to run on Apple Silicon, and the install script clears the download marker; a browser-downloaded
  archive shows a prompt, which the installation page states.
