@echo off
setlocal

cd /d "%~dp0"

echo Building codetty...
go build -o codetty.exe .\cmd\codetty
if errorlevel 1 exit /b 1

echo Done: codetty.exe

if "%1"=="install" (
    echo Installing...
    copy /y codetty.exe "%LOCALAPPDATA%\codetty\codetty.exe" >nul 2>&1
    echo Installed.
    echo Hot-upgrading daemon...
    codetty upgrade 2>nul && echo Daemon upgraded. || echo No daemon running ^(skipped^).
)
