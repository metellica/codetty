@echo off
setlocal

cd /d "%~dp0"

echo Building codetty...
go build -o codetty.exe .\cmd\codetty
if errorlevel 1 exit /b 1

echo Done: codetty.exe
