#Requires -Version 5.1
<#
.SYNOPSIS
    Install the Mailkube CLI on Windows.

.DESCRIPTION
    Downloads the release archive, verifies it against the published checksums, and installs the
    binary. Verification is not optional and there is no switch that skips it: a script that runs
    with permission to write to your PATH has no business extracting bytes it has not checked.

    When cosign is on PATH, the release signature is verified as well.

.PARAMETER Version
    The version to install, for example v1.2.3. Defaults to the latest release, or to
    $env:MAILKUBE_VERSION when that is set.

.PARAMETER InstallDir
    Where to put mailkube.exe. Defaults to $env:MAILKUBE_INSTALL_DIR, or to a per-user programs
    directory, which needs no elevation.

.EXAMPLE
    Invoke-WebRequest https://github.com/mailkube/mailkube-cli/releases/latest/download/install.ps1 -OutFile install.ps1
    .\install.ps1
#>
[CmdletBinding()]
param(
    [string] $Version = $env:MAILKUBE_VERSION,
    [string] $InstallDir = $env:MAILKUBE_INSTALL_DIR
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$InformationPreference = 'Continue'

$Repo = 'mailkube/mailkube-cli'
$Releases = "https://github.com/$Repo/releases"

# Keyless signatures are issued to the workflow run that produced them, so the identity to trust
# names that run: the publish workflow in this repository, on the default branch, with a
# certificate from GitHub's own issuer. The release is cut by a push to that branch and the tag is
# created during the run, so the certificate is issued against the branch and never against a tag.
#
# The trailing anchor is load-bearing, and is backtick-escaped because a bare $ before the closing
# quote of an interpolating string reads as the start of a variable. The publish workflow can also
# be started by hand from any branch, and without the anchor `main-anything` would satisfy this.
$CertIdentity = "^https://github\.com/$Repo/\.github/workflows/publish\.yml@refs/heads/main`$"
$CertIssuer = 'https://token.actions.githubusercontent.com'

function Write-Step {
    param([Parameter(Mandatory)] [string] $Message)
    Write-Information $Message
}

function Get-TargetArchitecture {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)." }
    }
}

function Get-LatestVersion {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
    if (-not $release.PSObject.Properties['tag_name']) {
        throw "Could not determine the latest version. Set MAILKUBE_VERSION to install a specific one."
    }
    return $release.tag_name
}

# Checksums are not optional. The hash is compared against the line the release published for this
# exact file name, so an archive that is not listed fails rather than being taken on trust.
function Test-Checksum {
    param(
        [Parameter(Mandatory)] [string] $Path,
        [Parameter(Mandatory)] [string] $ChecksumFile
    )

    $name = Split-Path -Leaf $Path
    $line = Select-String -Path $ChecksumFile -Pattern "\s$([regex]::Escape($name))$" | Select-Object -First 1
    if (-not $line) {
        throw "checksums.txt does not list $name. Refusing to install an unlisted file."
    }

    $expected = ($line.Line -split '\s+')[0]
    $actual = (Get-FileHash -Path $Path -Algorithm SHA256).Hash

    if ($actual -ne $expected.ToUpperInvariant()) {
        throw "Checksum mismatch for $name. Do not use this download."
    }
    Write-Step '+ Checksum verified'
}

# The signature covers the checksum file, which in turn covers every artifact, so one verification
# is enough to cover the archive. cosign is not required to install; when present, it is used.
function Test-Signature {
    param(
        [Parameter(Mandatory)] [string] $BaseUrl,
        [Parameter(Mandatory)] [string] $ChecksumFile
    )

    if (-not (Get-Command cosign -ErrorAction SilentlyContinue)) {
        Write-Step '. cosign not installed - checksum verified, signature not checked'
        Write-Step '  Install cosign to verify the release signature: https://docs.sigstore.dev/cosign/installation/'
        return
    }

    $dir = Split-Path -Parent $ChecksumFile
    $bundle = Join-Path $dir 'checksums.txt.sigstore.json'
    Invoke-WebRequest -Uri "$BaseUrl/checksums.txt.sigstore.json" -OutFile $bundle -UseBasicParsing

    # An external program's progress output arrives on the error stream, and with the preference
    # set to Stop that alone would abort here as though verification had failed. The exit code is
    # the verdict, so the preference is relaxed for exactly the length of the call, and the
    # captured output is shown only when the verdict is bad.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & cosign verify-blob $ChecksumFile `
            --bundle $bundle `
            --certificate-identity-regexp $CertIdentity `
            --certificate-oidc-issuer $CertIssuer 2>&1
    }
    finally {
        $ErrorActionPreference = $previous
    }

    if ($LASTEXITCODE -ne 0) {
        $output | ForEach-Object { Write-Step "  $_" }
        throw @'
Signature verification failed. Do not use this download.
The signature is a Sigstore bundle, which cosign reads from v3 onwards. If yours
is older, upgrading it is worth ruling out before treating this as tampering.
'@
    }
    Write-Step '+ Signature verified'
}

# Windows PowerShell defaults to a TLS version that the download host no longer accepts, and the
# failure it produces says nothing about TLS.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$arch = Get-TargetArchitecture
if (-not $Version) { $Version = Get-LatestVersion }
$number = $Version.TrimStart('v')

$archive = "mailkube_${number}_windows_${arch}.zip"
$baseUrl = "$Releases/download/$Version"

Write-Step "Installing mailkube $Version (windows/$arch)"

$work = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $work -Force | Out-Null

try {
    $archivePath = Join-Path $work $archive
    $checksumPath = Join-Path $work 'checksums.txt'

    Invoke-WebRequest -Uri "$baseUrl/$archive" -OutFile $archivePath -UseBasicParsing
    Invoke-WebRequest -Uri "$baseUrl/checksums.txt" -OutFile $checksumPath -UseBasicParsing

    Test-Checksum -Path $archivePath -ChecksumFile $checksumPath
    Test-Signature -BaseUrl $baseUrl -ChecksumFile $checksumPath

    # Verified, and only now extracted. Extracting first and checking afterwards would mean the
    # bytes had already been written somewhere by the time the check failed.
    $extracted = Join-Path $work 'extracted'
    Expand-Archive -Path $archivePath -DestinationPath $extracted -Force

    $exe = Join-Path $extracted 'mailkube.exe'
    if (-not (Test-Path $exe)) {
        throw 'The archive did not contain mailkube.exe.'
    }

    if (-not $InstallDir) {
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\mailkube'
    }
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Copy-Item -Path $exe -Destination (Join-Path $InstallDir 'mailkube.exe') -Force

    Write-Step "+ Installed $InstallDir\mailkube.exe"

    # The user PATH, not the machine PATH: this install needs no elevation and should not claim
    # any. A new terminal picks it up; the current one does not.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable('Path', "$userPath;$InstallDir", 'User')
        Write-Step ''
        Write-Step "Added $InstallDir to your PATH. Open a new terminal for it to take effect."
    }

    Write-Step ''
    Write-Step 'Next: mailkube init'
}
finally {
    # Removed whether this succeeded or failed, so a failed verification leaves no half-checked
    # archive behind for someone to find later and run.
    Remove-Item -Path $work -Recurse -Force -ErrorAction SilentlyContinue
}
