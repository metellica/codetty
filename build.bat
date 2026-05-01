@echo off
setlocal

cd /d "%~dp0"

echo Building codetty...
go build -o codetty-hub.exe .\cmd\codetty-hub
if errorlevel 1 exit /b 1

go build -o codetty-cli.exe .\cmd\codetty-cli
if errorlevel 1 exit /b 1

echo Done: codetty-hub.exe, codetty-cli.exe
