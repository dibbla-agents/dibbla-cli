# Dibbla CLI installer for Windows (PowerShell)
# Usage: irm https://raw.githubusercontent.com/dibbla-agents/dibbla-cli/main/install.ps1 | iex

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = "dibbla-agents/dibbla-cli"
$Binary = "dibbla"
$InstallDir = "$env:LOCALAPPDATA\dibbla"

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
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "dibbla-installer" }
        return $release.tag_name
    }
    catch {
        Write-Err "Failed to fetch latest version: $_"
        exit 1
    }
}

# Download and install
function Install-Dibbla {
    $arch = Get-Arch
    $version = Get-LatestVersion
    $versionNum = $version.TrimStart("v")

    $archiveName = "${Binary}_${versionNum}_windows_${arch}.zip"
    $downloadUrl = "https://github.com/$Repo/releases/download/$version/$archiveName"

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
        }

        # Verify
        Write-Host ""
        Write-Ok "  dibbla $version installed successfully!"
        Write-Host ""
        Write-Info "  Get started:"
        Write-Host "    dibbla create go-worker my-project"
        Write-Host ""
        Write-Warn "  Note: You may need to restart your terminal for PATH changes to take effect."
        Write-Host ""
    }
    finally {
        Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Install-Dibbla

