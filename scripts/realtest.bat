@echo off
rem Runs scripts\realtest.sh in this window, so every step shows here live.
rem   scripts\realtest.bat ["C:\path\to\Nabu Test system"] [prepare] [check] [serve]
rem Without a folder it uses NABU_TEST, else the checkout's "Nabu Test system".
rem Needs Git for Windows.
setlocal
set "BASH="
for %%G in (git.exe) do if not "%%~$PATH:G"=="" set "BASH=%%~dp$PATH:G..\bin\bash.exe"
if not exist "%BASH%" set "BASH=%ProgramW6432%\Git\bin\bash.exe"
if not exist "%BASH%" set "BASH=%ProgramFiles%\Git\bin\bash.exe"
if not exist "%BASH%" set "BASH=%LocalAppData%\Programs\Git\bin\bash.exe"
if not exist "%BASH%" (
  echo Git for Windows was not found: it brings the bash this test runs in.
  echo Install it from https://git-scm.com and run this again.
  goto end
)
chcp 65001 >nul
set "SCRIPT=%~dp0realtest.sh"
rem bash wants / in the script's path
"%BASH%" "%SCRIPT:\=/%" %*
set RC=%ERRORLEVEL%
echo.
echo Finished (exit code %RC%). The report is in the realtest folder inside the NABU folder.
:end
rem Double-clicked: keep the window open to read it.
echo %CMDCMDLINE% | find /i "/c" >nul && pause
endlocal & exit /b %RC%
