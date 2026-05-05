@echo off
setlocal

:: Lobster Windows Installer
:: Copies lobster.exe to %LOCALAPPDATA%\lobster and adds it to user PATH.

set "INSTALL_DIR=%LOCALAPPDATA%\lobster\bin"

echo Installing lobster to %INSTALL_DIR% ...

:: Create install directory
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"

:: Find lobster.exe in current directory
if not exist "%~dp0lobster.exe" (
    echo ERROR: lobster.exe not found in %~dp0
    echo Extract the zip first, then run install.bat from the same folder.
    pause
    exit /b 1
)

:: Copy binary
copy /Y "%~dp0lobster.exe" "%INSTALL_DIR%\lobster.exe" >nul
if errorlevel 1 (
    echo ERROR: Failed to copy lobster.exe
    pause
    exit /b 1
)

:: Check if already in PATH
echo %PATH% | findstr /I /C:"%INSTALL_DIR%" >nul
if %errorlevel%==0 (
    echo PATH already contains %INSTALL_DIR%
    goto :done
)

:: Add to user PATH permanently
for /f "tokens=2*" %%A in ('reg query "HKCU\Environment" /v Path 2^>nul') do set "USER_PATH=%%B"

if defined USER_PATH (
    setx PATH "%USER_PATH%;%INSTALL_DIR%" >nul
) else (
    setx PATH "%INSTALL_DIR%" >nul
)

echo Added %INSTALL_DIR% to user PATH.

:done
echo.
echo Lobster installed successfully!
echo Restart your terminal, then run: lobster version
echo.
pause
