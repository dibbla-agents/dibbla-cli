# Dibbla CLI installer for Windows (PowerShell)
# Usage: irm https://install.dibbla.com/install.ps1 | iex

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Binary = "dibbla"
$InstallDir = if ($env:DIBBLA_INSTALL_DIR) { $env:DIBBLA_INSTALL_DIR } else { "$env:LOCALAPPDATA\dibbla" }

# Every request this script makes goes to $BaseUrl and nowhere else. It used to
# resolve the version from api.github.com and download from github.com, both of
# which return 403 inside Anthropic-hosted agent sandboxes (the block is
# repository-scoped by Anthropic, not allowlistable by us or a customer), and
# github.com release assets additionally 302 to a third host. Kept in lockstep
# with install.sh. See P-0020.
#
# DIBBLA_INSTALL_BASE_URL points the installer at a staging deploy or a test
# fixture; it is what makes the mirror verifiable before DNS moves onto it.
$BaseUrl = if ($env:DIBBLA_INSTALL_BASE_URL) { $env:DIBBLA_INSTALL_BASE_URL.TrimEnd("/") } else { "https://install.dibbla.com" }

function Write-Info($msg)  { Write-Host $msg -ForegroundColor Cyan }
function Write-Ok($msg)    { Write-Host $msg -ForegroundColor Green }
function Write-Warn($msg)  { Write-Host $msg -ForegroundColor Yellow }
function Write-Err($msg)   { Write-Host $msg -ForegroundColor Red }

# Detect architecture
function Get-Arch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ($arch) {
        "X64"   { return "amd64" }
        "Arm64" { return "arm64" }
        default {
            # Fallback for older PowerShell versions
            if ($env:PROCESSOR_ARCHITECTURE -eq "AMD64") { return "amd64" }
            if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { return "arm64" }
            Write-Err "Unsupported architecture: $arch"
            exit 1
        }
    }
}

# Get latest release version
function Get-LatestVersion {
    try {
        $release = Invoke-RestMethod -Uri "$BaseUrl/latest.json" -Headers @{ "User-Agent" = "dibbla-installer" }
        return $release.tag_name
    }
    catch {
        Write-Err "Failed to fetch latest version from $BaseUrl/latest.json: $_"
        exit 1
    }
}

# Verify the downloaded archive against the mirror's checksums.txt.
#
# New in P-0020, and it closes a real gap: until now a first install expanded a
# zip with no verification at all. It was unavoidable while checksums.txt was one
# more GitHub asset behind the same 403; now that it is served from the same
# origin as the archive, it is four lines.
#
# Fail-closed with no opt-out, matching install.sh -- a skippable checksum is
# decoration. Get-FileHash ships with PowerShell 4+, so there is no
# missing-tool branch to worry about on this side.
function Assert-Checksum($archivePath, $archiveName) {
    try {
        $sums = Invoke-RestMethod -Uri "$BaseUrl/checksums.txt" -Headers @{ "User-Agent" = "dibbla-installer" }
    }
    catch {
        Write-Err "  Failed to fetch $BaseUrl/checksums.txt -- refusing to install unverified: $_"
        exit 1
    }

    # goreleaser writes "<sha256>  <name>"; the name is occasionally prefixed
    # with "*" for binary mode.
    $expected = $null
    foreach ($line in ($sums -split "`n")) {
        $fields = $line.Trim() -split '\s+'
        if ($fields.Count -ge 2 -and $fields[1].TrimStart("*") -eq $archiveName) {
            $expected = $fields[0].ToLower()
            break
        }
    }
    if (-not $expected) {
        Write-Err "  checksums.txt has no entry for $archiveName -- refusing to install unverified."
        exit 1
    }

    $actual = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
    if ($expected -ne $actual) {
        Write-Err "  Checksum mismatch for $archiveName!"
        Write-Err "    expected $expected"
        Write-Err "    actual   $actual"
        Write-Err "  Refusing to install. Nothing was written to $InstallDir."
        exit 1
    }

    Write-Ok "  Verified SHA-256."
}

# Download and install
function Install-Dibbla {
    $arch = Get-Arch
    $version = Get-LatestVersion
    $versionNum = $version.TrimStart("v")

    $archiveName = "${Binary}_${versionNum}_windows_${arch}.zip"
    $downloadUrl = "$BaseUrl/latest/$archiveName"

    $tmpDir = Join-Path $env:TEMP "dibbla-install-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

    try {
        Write-Host ""
        Write-Info "  Dibbla CLI Installer"
        Write-Info "  --------------------"
        Write-Host ""

        Write-Info "  Downloading dibbla $version for windows/$arch..."
        $archivePath = Join-Path $tmpDir $archiveName
        Invoke-WebRequest -Uri $downloadUrl -OutFile $archivePath -UseBasicParsing

        Write-Info "  Verifying..."
        Assert-Checksum $archivePath $archiveName

        Write-Info "  Extracting..."
        Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force

        # Create install directory
        if (-not (Test-Path $InstallDir)) {
            New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        }

        # Copy binary
        $binaryPath = Join-Path $tmpDir "$Binary.exe"
        Copy-Item -Path $binaryPath -Destination (Join-Path $InstallDir "$Binary.exe") -Force

        # Add to user PATH if not already there
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        if ($userPath -notlike "*$InstallDir*") {
            Write-Info "  Adding $InstallDir to your PATH..."
            [Environment]::SetEnvironmentVariable("Path", "$userPath;$InstallDir", "User")
            # Also update current session
            $env:Path = "$env:Path;$InstallDir"

            # Broadcast WM_SETTINGCHANGE so Explorer and other running apps pick
            # up the new user PATH without requiring a logout. Best-effort:
            # swallow any failure -- the PATH update itself already succeeded.
            try {
                if (-not ('Win32.NativeMethods' -as [Type])) {
                    Add-Type -Namespace Win32 -Name NativeMethods -MemberDefinition @"
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true, CharSet = System.Runtime.InteropServices.CharSet.Auto)]
public static extern System.IntPtr SendMessageTimeout(
    System.IntPtr hWnd, uint Msg, System.UIntPtr wParam, string lParam,
    uint fuFlags, uint uTimeout, out System.UIntPtr lpdwResult);
"@
                }
                $HWND_BROADCAST = [IntPtr]0xffff
                $WM_SETTINGCHANGE = 0x1A
                $result = [UIntPtr]::Zero
                [void][Win32.NativeMethods]::SendMessageTimeout(
                    $HWND_BROADCAST, $WM_SETTINGCHANGE, [UIntPtr]::Zero, "Environment",
                    2, 5000, [ref]$result)
            } catch {
                # Non-fatal. New shells will still pick up the change.
            }
        }

        # Verify
        Write-Host ""
        Write-Ok "  dibbla $version installed successfully!"
        Write-Host ""
        Write-Info "  Get started:"
        Write-Host "    dibbla init"
        Write-Host ""
        Write-Warn "  Note: You may need to restart your terminal for PATH changes to take effect."
        Write-Host ""
    }
    finally {
        Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# First tag that shipped `dibbla update` (internal/cmd/update/update.go).
# Only delegate to the existing binary's self-update when it's at least
# this version; anything older predates the command.
$MinUpdateVersion = [version]"1.2.10"

# Parse the X.Y.Z triple from `dibbla --version` (prints "dibbla version
# X.Y.Z" to stdout only -- no stderr, so it can't trip the $ErrorAction-
# Preference='Stop' native-command trap the way an unknown subcommand does).
# Returns $null for a dev/unparseable build, which the caller treats as
# "reinstall from scratch".
function Get-ExistingVersion($exe) {
    try {
        $prev = $ErrorActionPreference
        $ErrorActionPreference = 'SilentlyContinue'
        $out = & $exe --version 2>$null
        $ErrorActionPreference = $prev
    } catch { return $null }
    if ($out -match '(\d+)\.(\d+)\.(\d+)') {
        return [version]("{0}.{1}.{2}" -f $matches[1], $matches[2], $matches[3])
    }
    return $null
}

# If a working dibbla is already on PATH and is new enough to know the
# `update` subcommand (v1.2.10+), delegate to it. That path does the
# running-exe rename dance and the install-method detection -- still the
# things this bootstrap script can't do on its own. (SHA-256 verification
# used to be on that list too; since P-0020 mirrored checksums.txt onto the
# same origin as the archive, Assert-Checksum above does it here as well.)
# We gate on the
# parsed `--version` number rather than probing `update --help`: the probe
# made an old binary write to stderr, which under $ErrorActionPreference =
# "Stop" became a terminating error that aborted the installer instead of
# falling through. A version gate is also strictly conservative -- a dev or
# unparseable build simply reinstalls.
#
# Set $env:DIBBLA_INSTALLER_FORCE=1 to skip delegation (useful when an
# existing `dibbla update` is broken or when targeting a new install dir).
function Invoke-MaybeDelegateToUpdate {
    if ($env:DIBBLA_INSTALLER_FORCE -eq "1") { return }

    $existing = Get-Command dibbla -ErrorAction SilentlyContinue
    if (-not $existing) { return }

    $ver = Get-ExistingVersion $existing.Source
    if ($null -eq $ver -or $ver -lt $MinUpdateVersion) { return }

    Write-Host ""
    Write-Info "  Found existing dibbla $ver at $($existing.Source) -- delegating to 'dibbla update'."
    Write-Info "  (set `$env:DIBBLA_INSTALLER_FORCE=1 to reinstall from scratch instead.)"
    Write-Host ""
    & $existing.Source update --yes
    exit $LASTEXITCODE
}

Invoke-MaybeDelegateToUpdate
Install-Dibbla

