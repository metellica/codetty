@echo off
setlocal

cd /d "%~dp0"

echo Building codetty...
for /f "delims=" %%v in ('git describe --tags --always --dirty 2^>nul') do set VERSION=%%v
if "%VERSION%"=="" set VERSION=dev
for /f "delims=" %%t in ('powershell -NoProfile -Command "(Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')"') do set BUILD_TIME=%%t

set CGO_ENABLED=0
go build -trimpath -ldflags "-X main.version=%VERSION% -X main.buildTime=%BUILD_TIME%" -o codetty.exe .\cmd\codetty
if errorlevel 1 exit /b 1

echo Done: codetty.exe %VERSION% (built %BUILD_TIME%)

if "%1"=="install" (
    echo Installing...
    copy /y codetty.exe "%LOCALAPPDATA%\codetty\codetty.exe" >nul 2>&1
    echo Installed.
    echo Hot-upgrading daemon...
    codetty upgrade 2>nul && echo Daemon upgraded. || echo No daemon running ^(skipped^).
)
