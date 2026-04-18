# Dibbla CLI installer for Windows (PowerShell)
# Usage: irm https://install.dibbla.com/install.ps1 | iex

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = "dibbla-agents/dibbla-cli"
$Binary = "dibbla"
$InstallDir = if ($env:DIBBLA_INSTALL_DIR) { $env:DIBBLA_INSTALL_DIR } else { "$env:LOCALAPPDATA\dibbla" }

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

            # Broadcast WM_SETTINGCHANGE so Explorer and other running apps pick
            # up the new user PATH without requiring a logout. Best-effort:
            # swallow any failure — the PATH update itself already succeeded.
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

