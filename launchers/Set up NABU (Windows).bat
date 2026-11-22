@echo off
rem  NABU setup. Double-click this once the computer is online: it gets the
rem  newest English Wikipedia onto this drive with everything it needs, with a
rem  timer for each step, then starts NABU. Run it again to carry on or update.
setlocal
title NABU setup
cd /d "%~dp0"

rem Pick the program that matches this processor.
set "ARCH=amd64"
if /i "%PROCESSOR_ARCHITECTURE%"=="x86" if not defined PROCESSOR_ARCHITEW6432 set "ARCH=386"
if /i "%PROCESSOR_ARCHITECTURE%"=="ARM64" set "ARCH=arm64"
if /i "%PROCESSOR_ARCHITEW6432%"=="ARM64" set "ARCH=arm64"
set "BIN=%~dp0programs\windows\nabu-windows-%ARCH%.exe"

rem Windows 7, 8 and 8.1 need the legacy build.
set "WINMAJOR="
for /f "tokens=4 delims=. []" %%i in ('ver') do set "WINMAJOR=%%i"
if defined WINMAJOR if %WINMAJOR% LSS 10 set "BIN=%~dp0programs\windows\nabu-windows-386-legacy.exe"

if not exist "%BIN%" (
  echo NABU could not find its program file:
  echo   %BIN%
  echo This copy of NABU has no programs for this computer. Copy the
  echo "programs\windows" folder from a complete NABU drive next to this file.
  pause
  exit /b 1
)

"%BIN%" setup --drive "%~dp0." %*
pause
