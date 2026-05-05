@echo off
setlocal EnableDelayedExpansion

:: Lobster Windows Installer
:: Installs lobster.exe, adds to PATH, and installs dependencies via winget.

echo.
echo  Lobster Installer for Windows
echo  ==============================
echo.

set "INSTALL_DIR=%LOCALAPPDATA%\lobster\bin"

:: --- Step 1: Install lobster binary ---

echo [1/3] Installing lobster ...

if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"

if not exist "%~dp0lobster.exe" (
    echo ERROR: lobster.exe not found in %~dp0
    echo Extract the zip first, then run install.bat from the same folder.
    pause
    exit /b 1
)

copy /Y "%~dp0lobster.exe" "%INSTALL_DIR%\lobster.exe" >nul
if errorlevel 1 (
    echo ERROR: Failed to copy lobster.exe
    pause
    exit /b 1
)

echo        Copied to %INSTALL_DIR%

:: --- Step 2: Add to PATH ---

echo [2/3] Configuring PATH ...

echo %PATH% | findstr /I /C:"%INSTALL_DIR%" >nul
if %errorlevel%==0 (
    echo        Already in PATH.
) else (
    for /f "tokens=2*" %%A in ('reg query "HKCU\Environment" /v Path 2^>nul') do set "USER_PATH=%%B"

    if defined USER_PATH (
        setx PATH "!USER_PATH!;%INSTALL_DIR%" >nul
    ) else (
        setx PATH "%INSTALL_DIR%" >nul
    )
    echo        Added to user PATH.
)

:: --- Step 3: Install dependencies ---

echo [3/3] Checking dependencies ...

:: Check for winget
where winget >nul 2>&1
if %errorlevel% neq 0 (
    echo.
    echo WARNING: winget not found. Install dependencies manually:
    echo   - mpv:    https://mpv.io/installation/
    echo   - fzf:    https://github.com/junegunn/fzf/releases
    echo   - ffmpeg: https://ffmpeg.org/download.html
    echo.
    goto :done
)

:: mpv (required - media player)
where mpv >nul 2>&1
if %errorlevel% neq 0 (
    echo        Installing mpv ...
    winget install --id mpv-player.mpv --accept-source-agreements --accept-package-agreements >nul 2>&1
    if !errorlevel! equ 0 (
        echo        mpv installed.
    ) else (
        echo        mpv install failed. Install manually: winget install mpv-player.mpv
    )
) else (
    echo        mpv found.
)

:: fzf (required - interactive menus)
where fzf >nul 2>&1
if %errorlevel% neq 0 (
    echo        Installing fzf ...
    winget install --id junegunn.fzf --accept-source-agreements --accept-package-agreements >nul 2>&1
    if !errorlevel! equ 0 (
        echo        fzf installed.
    ) else (
        echo        fzf install failed. Install manually: winget install junegunn.fzf
    )
) else (
    echo        fzf found.
)

:: ffmpeg (optional - downloads)
where ffmpeg >nul 2>&1
if %errorlevel% neq 0 (
    echo        Installing ffmpeg ...
    winget install --id Gyan.FFmpeg --accept-source-agreements --accept-package-agreements >nul 2>&1
    if !errorlevel! equ 0 (
        echo        ffmpeg installed.
    ) else (
        echo        ffmpeg install failed. Install manually: winget install Gyan.FFmpeg
    )
) else (
    echo        ffmpeg found.
)

:done
echo.
echo  Installation complete!
echo  Restart your terminal, then run: lobster version
echo.
pause
