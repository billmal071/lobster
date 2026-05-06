# Lobster Windows Installer
# Works as standalone (downloads from GitHub) or from the release zip.
# One-liner: irm https://raw.githubusercontent.com/billmal071/lobster/main/install.ps1 | iex

$ErrorActionPreference = "Stop"
$Repo = "billmal071/lobster"
$InstallDir = "$env:LOCALAPPDATA\lobster\bin"
$DepsDir = "$env:LOCALAPPDATA\lobster\deps"

Write-Host ""
Write-Host "  Lobster Installer for Windows" -ForegroundColor Cyan
Write-Host "  ==============================" -ForegroundColor Cyan
Write-Host ""

if (!(Test-Path $InstallDir)) { New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null }
if (!(Test-Path $DepsDir)) { New-Item -ItemType Directory -Path $DepsDir -Force | Out-Null }

# --- Step 1: Install lobster binary ---
Write-Host "[1/4] Installing lobster ..." -ForegroundColor Yellow

$scriptDir = if ($MyInvocation.MyCommand.Path) { Split-Path -Parent $MyInvocation.MyCommand.Path } else { "" }
$localExe = if ($scriptDir) { Join-Path $scriptDir "lobster.exe" } else { "" }

if ($localExe -and (Test-Path $localExe)) {
    # Running from extracted zip
    Copy-Item $localExe "$InstallDir\lobster.exe" -Force
    Write-Host "  Copied from zip to $InstallDir" -ForegroundColor Green
} else {
    # Standalone mode: download latest release
    Write-Host "  Fetching latest release ..."
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
    $tag = $release.tag_name
    Write-Host "  Latest: $tag"

    $winArch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "x86_64" }
    $assetName = "lobster_Windows_${winArch}.zip"
    $asset = $release.assets | Where-Object { $_.name -eq $assetName }

    if (!$asset) {
        Write-Host "  ERROR: Asset $assetName not found in release $tag" -ForegroundColor Red
        Write-Host "  The release may still be building. Try again in a few minutes."
        return
    }

    $zipPath = Join-Path $env:TEMP "lobster-download.zip"
    $extractDir = Join-Path $env:TEMP "lobster-extract"

    Write-Host "  Downloading $assetName ..."
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zipPath -UseBasicParsing

    if (Test-Path $extractDir) { Remove-Item $extractDir -Recurse -Force }
    Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force

    $exe = Get-ChildItem -Path $extractDir -Recurse -Filter "lobster.exe" | Select-Object -First 1
    if (!$exe) {
        Write-Host "  ERROR: lobster.exe not found in download" -ForegroundColor Red
        return
    }

    Copy-Item $exe.FullName "$InstallDir\lobster.exe" -Force
    Remove-Item $zipPath -Force -ErrorAction SilentlyContinue
    Remove-Item $extractDir -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host "  Installed $tag to $InstallDir" -ForegroundColor Green
}

# --- Step 2: Add to PATH ---
Write-Host "[2/4] Configuring PATH ..." -ForegroundColor Yellow

$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
$pathChanged = $false

foreach ($dir in @($InstallDir, $DepsDir)) {
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

# --- Step 3: Install dependencies ---
Write-Host "[3/4] Installing dependencies ..." -ForegroundColor Yellow

function Install-Dep {
    param($Name, $Url, $ExeName)

    $dest = Join-Path $DepsDir $ExeName
    if (Test-Path $dest) {
        Write-Host "  $Name already installed." -ForegroundColor Green
        return
    }

    if (Get-Command winget -ErrorAction SilentlyContinue) {
        $wingetId = switch ($Name) {
            "mpv"    { "mpv-player.mpv" }
            "fzf"    { "junegunn.fzf" }
            "ffmpeg" { "Gyan.FFmpeg" }
        }
        Write-Host "  Trying winget for $Name ..."
        winget install --id $wingetId --accept-source-agreements --accept-package-agreements --silent 2>$null | Out-Null
        $freshPath = [Environment]::GetEnvironmentVariable("PATH", "Machine") + ";" + [Environment]::GetEnvironmentVariable("PATH", "User")
        $env:PATH = $freshPath + ";$InstallDir;$DepsDir"
        if (Get-Command $Name -ErrorAction SilentlyContinue) {
            Write-Host "  $Name installed via winget." -ForegroundColor Green
            return
        }
    }

    if ($Url) {
        Write-Host "  Downloading $Name directly ..."
        $zipPath = Join-Path $env:TEMP "$Name-download.zip"
        try {
            Invoke-WebRequest -Uri $Url -OutFile $zipPath -UseBasicParsing
            $extractDir = Join-Path $env:TEMP "$Name-extract"
            if (Test-Path $extractDir) { Remove-Item $extractDir -Recurse -Force }
            Expand-Archive -Path $zipPath -DestinationPath $extractDir -Force
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

$winArch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$fzfUrl = "https://github.com/junegunn/fzf/releases/download/v0.62.0/fzf-0.62.0-windows_${winArch}.zip"

if (!(Get-Command fzf -ErrorAction SilentlyContinue)) {
    Install-Dep -Name "fzf" -Url $fzfUrl -ExeName "fzf.exe"
} else { Write-Host "  fzf found." -ForegroundColor Green }

if (!(Get-Command mpv -ErrorAction SilentlyContinue)) {
    Install-Dep -Name "mpv" -Url $null -ExeName "mpv.exe"
} else { Write-Host "  mpv found." -ForegroundColor Green }

if (!(Get-Command ffmpeg -ErrorAction SilentlyContinue)) {
    Install-Dep -Name "ffmpeg" -Url $null -ExeName "ffmpeg.exe"
} else { Write-Host "  ffmpeg found." -ForegroundColor Green }

# --- Step 4: Migrate old config ---
Write-Host "[4/4] Verifying ..." -ForegroundColor Yellow

$configFile = "$env:APPDATA\lobster\config.toml"
if (Test-Path $configFile) {
    $content = Get-Content $configFile -Raw
    if ($content -match '(?m)^base\s*=') {
        $content = $content -replace '(?m)^base\s*=.*\r?\n?', ''
        Set-Content $configFile $content
        Write-Host "  Migrated config: removed old provider setting" -ForegroundColor Green
    }
}

$allGood = $true
foreach ($cmd in @("lobster", "fzf")) {
    if (Get-Command $cmd -ErrorAction SilentlyContinue) {
        Write-Host "  $cmd ... OK" -ForegroundColor Green
    } else {
        Write-Host "  $cmd ... NOT FOUND" -ForegroundColor Red
        $allGood = $false
    }
}
foreach ($cmd in @("mpv", "ffmpeg")) {
    if (Get-Command $cmd -ErrorAction SilentlyContinue) {
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
