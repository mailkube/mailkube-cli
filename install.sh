#!/bin/sh
# Install the Mailkube CLI.
#
#   curl -fsSL https://github.com/mailkube/mailkube-cli/releases/latest/download/install.sh -o install.sh
#   sh install.sh
#
# The download is verified before anything is extracted, and there is no way to turn that off.
# A flag that skips verification is a flag that ends up in the answer to "it does not work on my
# machine", and a script that runs as the user who can write to /usr/local/bin has no business
# extracting bytes it has not checked.
#
# Environment:
#   MAILKUBE_VERSION      version to install, e.g. v1.2.3   (default: the latest release)
#   MAILKUBE_INSTALL_DIR  where to put the binary           (default: /usr/local/bin, else ~/.local/bin)
set -eu

REPO="mailkube/mailkube-cli"
RELEASES="https://github.com/${REPO}/releases"
API="https://api.github.com/repos/${REPO}/releases/latest"

# Keyless signatures are issued to the workflow that produced them, so the identity to trust is a
# workflow in this repository, running on a tag, with a certificate from GitHub's own issuer.
CERT_IDENTITY="^https://github.com/${REPO}/\.github/workflows/.+@refs/tags/"
CERT_ISSUER="https://token.actions.githubusercontent.com"

say() { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }

die() {
  printf '✗ %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required and was not found."
}

# ---------------------------------------------------------------------------- download

download() {
  url=$1
  dest=$2
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  else
    wget -q -O "$dest" "$url"
  fi
}

# ---------------------------------------------------------------------------- platform

detect_os() {
  os=$(uname -s)
  case "$os" in
  Linux) printf 'linux' ;;
  Darwin) printf 'darwin' ;;
  *) die "Unsupported operating system: $os. Windows is installed with install.ps1." ;;
  esac
}

# macOS builds are Apple Silicon only.
#
# `uname -m` cannot answer that on its own: a shell running under translation on an Apple Silicon
# Mac reports x86_64, so asking it would turn away the exact machines this supports. The hardware
# capability is the thing to ask about.
detect_arch() {
  if [ "$1" = "darwin" ]; then
    if [ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = "1" ]; then
      printf 'arm64'
      return
    fi
    die "mailkube is built for Apple Silicon Macs. On an Intel Mac, build it from source:
  go install github.com/mailkube/mailkube-cli/cmd/mailkube@latest"
  fi
  arch=$(uname -m)
  case "$arch" in
  x86_64 | amd64) printf 'amd64' ;;
  aarch64 | arm64) printf 'arm64' ;;
  armv7l | armv7 | armhf) printf 'armv7' ;;
  *) die "Unsupported architecture: $arch." ;;
  esac
}

resolve_version() {
  if [ -n "${MAILKUBE_VERSION:-}" ]; then
    printf '%s' "$MAILKUBE_VERSION"
    return
  fi
  # The redirect target of /releases/latest carries the tag, which avoids spending an
  # unauthenticated API call that a shared address can easily have exhausted.
  if command -v curl >/dev/null 2>&1; then
    tag=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${RELEASES}/latest" | sed 's|.*/tag/||')
  else
    tag=$(download "$API" - | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
  fi
  [ -n "$tag" ] || die "Could not determine the latest version. Set MAILKUBE_VERSION to install a specific one."
  printf '%s' "$tag"
}

# ---------------------------------------------------------------------------- verification

# Checksums are not optional. Without a checksum tool the script stops rather than installing
# something it cannot vouch for.
#
# The tool is selected into the positional parameters rather than returned from a function: a
# function that calls `die` inside a command substitution exits the substitution's subshell, not
# the script, so the caller would carry on and report a verification that never ran.
verify_checksum() {
  archive=$1
  line=$(grep " ${archive}\$" checksums.txt) ||
    die "checksums.txt does not list ${archive}. Refusing to install an unlisted file."

  if command -v sha256sum >/dev/null 2>&1; then
    set -- sha256sum -c -
  elif command -v shasum >/dev/null 2>&1; then
    set -- shasum -a 256 -c -
  else
    die "No SHA-256 tool found (sha256sum or shasum). Install one, or download and verify the release by hand: ${RELEASES}"
  fi

  printf '%s\n' "$line" | "$@" >/dev/null ||
    die "Checksum mismatch for ${archive}. Do not use this download."

  say "✓ Checksum verified"
}

# The signature covers the checksum file, which in turn covers every artifact, so one verification
# is enough to cover the archive. cosign is not required to install; when it is present, it is used.
verify_signature() {
  base=$1
  if ! command -v cosign >/dev/null 2>&1; then
    say "· cosign not installed. Checksum verified, signature not checked."
    say "  Install cosign to verify the release signature: https://docs.sigstore.dev/cosign/installation/"
    return
  fi

  download "${base}/checksums.txt.sigstore.json" checksums.txt.sigstore.json

  cosign verify-blob checksums.txt \
    --bundle checksums.txt.sigstore.json \
    --certificate-identity-regexp "$CERT_IDENTITY" \
    --certificate-oidc-issuer "$CERT_ISSUER" >/dev/null 2>&1 ||
    die "Signature verification failed. Do not use this download.
  The signature is a Sigstore bundle, which cosign reads from v3 onwards. If yours
  is older, upgrading it is worth ruling out before treating this as tampering."

  say "✓ Signature verified"
}

# ---------------------------------------------------------------------------- install

target_dir() {
  if [ -n "${MAILKUBE_INSTALL_DIR:-}" ]; then
    printf '%s' "$MAILKUBE_INSTALL_DIR"
  elif [ -w /usr/local/bin ]; then
    printf '/usr/local/bin'
  else
    printf '%s/.local/bin' "$HOME"
  fi
}

main() {
  need uname
  need tar
  command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 ||
    die "curl or wget is required and neither was found."

  os=$(detect_os)
  arch=$(detect_arch "$os")
  version=$(resolve_version)
  number=${version#v}

  archive="mailkube_${number}_${os}_${arch}.tar.gz"
  base_url="${RELEASES}/download/${version}"

  say "Installing mailkube ${version} (${os}/${arch})"

  work=$(mktemp -d)
  # The working directory is removed whether this succeeds or fails, so a failed verification
  # leaves no half-checked archive behind for someone to find later and run.
  trap 'rm -rf "$work"' EXIT INT TERM
  cd "$work"

  download "${base_url}/${archive}" "$archive" ||
    die "Could not download ${archive}. Check that ${version} exists: ${RELEASES}"
  download "${base_url}/checksums.txt" checksums.txt ||
    die "Could not download checksums.txt. Refusing to install without it."

  verify_checksum "$archive"
  verify_signature "$base_url"

  # Verified, and only now extracted. Extracting first and checking afterwards would mean the
  # bytes had already been written somewhere by the time the check failed.
  tar -xzf "$archive"
  [ -f mailkube ] || die "The archive did not contain a mailkube binary."

  # Quarantine is set by the application that downloads a file, so this is a no-op on the path
  # above. It earns its place for an archive that arrived some other way, where macOS would
  # otherwise refuse to run the extracted binary with no useful explanation.
  if [ "$os" = "darwin" ] && command -v xattr >/dev/null 2>&1; then
    xattr -d com.apple.quarantine mailkube 2>/dev/null || true
  fi

  dir=$(target_dir)
  mkdir -p "$dir" ||
    die "Could not create ${dir}. Set MAILKUBE_INSTALL_DIR to a directory you can write to."
  chmod 0755 mailkube
  mv mailkube "$dir/mailkube" ||
    die "Could not write to ${dir}. Set MAILKUBE_INSTALL_DIR to a directory you can write to."

  say "✓ Installed ${dir}/mailkube"

  case ":${PATH}:" in
  *":${dir}:"*) ;;
  *)
    warn ""
    warn "${dir} is not on your PATH. Add it:"
    warn "  export PATH=\"${dir}:\$PATH\""
    ;;
  esac

  say ""
  say "Next: mailkube init"
}

main "$@"
