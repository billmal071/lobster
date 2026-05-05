# Lobster Windows Installer
# Installs lobster + dependencies (mpv, fzf, ffmpeg)

$ErrorActionPreference = "Stop"
$InstallDir = "$env:LOCALAPPDATA\lobster\bin"
$DepsDir = "$env:LOCALAPPDATA\lobster\deps"

Write-Host ""
Write-Host "  Lobster Installer for Windows" -ForegroundColor Cyan
Write-Host "  ==============================" -ForegroundColor Cyan
Write-Host ""

# --- Step 1: Install lobster binary ---
Write-Host "[1/4] Installing lobster ..." -ForegroundColor Yellow

if (!(Test-Path $InstallDir)) { New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null }
if (!(Test-Path $DepsDir)) { New-Item -ItemType Directory -Path $DepsDir -Force | Out-Null }

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$lobsterExe = Join-Path $scriptDir "lobster.exe"

if (!(Test-Path $lobsterExe)) {
    Write-Host "  ERROR: lobster.exe not found in $scriptDir" -ForegroundColor Red
    Write-Host "  Extract the zip first, then run install.ps1 from the same folder."
    Read-Host "Press Enter to exit"
    exit 1
}

Copy-Item $lobsterExe "$InstallDir\lobster.exe" -Force
Write-Host "  Copied to $InstallDir" -ForegroundColor Green

# --- Step 2: Add to PATH ---
Write-Host "[2/4] Configuring PATH ..." -ForegroundColor Yellow

$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
$pathDirs = @($InstallDir, $DepsDir)

foreach ($dir in $pathDirs) {
    if ($userPath -notlike "*$dir*") {
        $userPath = "$userPath;$dir"
        $pathChanged = $true
    }
}

if ($pathChanged) {
    [Environment]::SetEnvironmentVariable("PATH", $userPath, "User")
    $env:PATH = "$env:PATH;$InstallDir;$DepsDir"
    Write-Host "  Added to user PATH." -ForegroundColor Green
} else {
    Write-Host "  Already in PATH." -ForegroundColor Green
}

# --- Helper: download and extract ---
function Install-Dep {
    param($Name, $Url, $ExeName)

    $dest = Join-Path $DepsDir $ExeName
    if (Test-Path $dest) {
        Write-Host "  $Name already installed." -ForegroundColor Green
        return
    }

    # Try winget first
    $wingetId = switch ($Name) {
        "mpv"    { "mpv-player.mpv" }
        "fzf"    { "junegunn.fzf" }
        "ffmpeg" { "Gyan.FFmpeg" }
    }

    if (Get-Command winget -ErrorAction SilentlyContinue) {
        Write-Host "  Trying winget for $Name ..."
        winget install --id $wingetId --accept-source-agreements --accept-package-agreements --silent 2>$null | Out-Null
        # Refresh PATH to check
        $freshPath = [Environment]::GetEnvironmentVariable("PATH", "Machine") + ";" + [Environment]::GetEnvironmentVariable("PATH", "User")
        $env:PATH = $freshPath + ";$InstallDir;$DepsDir"
        if (Get-Command $Name -ErrorAction SilentlyContinue) {
            Write-Host "  $Name installed via winget." -ForegroundColor Green
            return
        }
    }

    # Fallback: direct download
    if ($Url) {
        Write-Host "  Downloading $Name directly ..."
        $zipPath = Join-Path $env:TEMP "$Name-download.zip"
        try {
            Invoke-WebRequest -Uri $Url -OutFile $zipPath -UseBasicParsing
            $extractDir = Join-Path $env:TEMP "$Name-extract"
            if (Test-Path $extractDir) { Remove-Item $extractDir -Recurse -Force }
            Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force

            # Find the executable recursively
            $found = Get-ChildItem -Path $extractDir -Recurse -Filter $ExeName | Select-Object -First 1
            if ($found) {
                Copy-Item $found.FullName $dest -Force
                Write-Host "  $Name installed to $DepsDir" -ForegroundColor Green
            } else {
                Write-Host "  Could not find $ExeName in download." -ForegroundColor Red
            }

            Remove-Item $zipPath -Force -ErrorAction SilentlyContinue
            Remove-Item $extractDir -Recurse -Force -ErrorAction SilentlyContinue
        } catch {
            Write-Host "  Download failed: $_" -ForegroundColor Red
            Write-Host "  Install $Name manually and add to PATH." -ForegroundColor Yellow
        }
    } else {
        Write-Host "  Could not install $Name. Install manually." -ForegroundColor Red
    }
}

# --- Step 3: Check and install dependencies ---
Write-Host "[3/4] Installing dependencies ..." -ForegroundColor Yellow

# Detect architecture
$arch = if ([Environment]::Is64BitOperatingSystem) { "64" } else { "32" }
$winArch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

# fzf - single binary, easy direct download
$fzfUrl = "https://github.com/junegunn/fzf/releases/latest/download/fzf-${fzfVersion}-windows_${winArch}.zip"
# Use a known recent version as fallback
$fzfUrl = "https://github.com/junegunn/fzf/releases/download/v0.62.0/fzf-0.62.0-windows_${winArch}.zip"
if (!(Get-Command fzf -ErrorAction SilentlyContinue)) {
    Install-Dep -Name "fzf" -Url $fzfUrl -ExeName "fzf.exe"
} else {
    Write-Host "  fzf found." -ForegroundColor Green
}

# mpv
if (!(Get-Command mpv -ErrorAction SilentlyContinue)) {
    Install-Dep -Name "mpv" -Url $null -ExeName "mpv.exe"
} else {
    Write-Host "  mpv found." -ForegroundColor Green
}

# ffmpeg
if (!(Get-Command ffmpeg -ErrorAction SilentlyContinue)) {
    Install-Dep -Name "ffmpeg" -Url $null -ExeName "ffmpeg.exe"
} else {
    Write-Host "  ffmpeg found." -ForegroundColor Green
}

# --- Step 4: Verify ---
Write-Host "[4/4] Verifying ..." -ForegroundColor Yellow

$allGood = $true
foreach ($cmd in @("lobster", "fzf")) {
    $found = Get-Command $cmd -ErrorAction SilentlyContinue
    if ($found) {
        Write-Host "  $cmd ... OK" -ForegroundColor Green
    } else {
        Write-Host "  $cmd ... NOT FOUND" -ForegroundColor Red
        $allGood = $false
    }
}

foreach ($cmd in @("mpv", "ffmpeg")) {
    $found = Get-Command $cmd -ErrorAction SilentlyContinue
    if ($found) {
        Write-Host "  $cmd ... OK" -ForegroundColor Green
    } else {
        Write-Host "  $cmd ... NOT FOUND (install via: winget install $cmd)" -ForegroundColor Yellow
    }
}

Write-Host ""
if ($allGood) {
    Write-Host "  Installation complete!" -ForegroundColor Green
} else {
    Write-Host "  Installed with warnings. Check missing dependencies above." -ForegroundColor Yellow
}
Write-Host "  Restart your terminal, then run: lobster version" -ForegroundColor Cyan
Write-Host ""
Read-Host "Press Enter to exit"
