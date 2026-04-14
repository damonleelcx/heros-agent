@echo off
setlocal EnableExtensions
title Install Heros Desktop
cd /d "%~dp0.."

echo.
echo === Heros Desktop installer (Windows) ===
echo This will run: go install ./cmd/heros-desktop
echo Then add Go's bin folder to your user PATH (same as: heros-desktop -add-path^)
echo.

where go >nul 2>nul
if errorlevel 1 (
  echo [ERROR] Go is not on PATH.
  echo Install Go from https://go.dev/dl/ then open a new Command Prompt and run this script again.
  echo.
  pause
  exit /b 1
)

echo Using:
go version
echo.

go install ./cmd/heros-desktop
if errorlevel 1 (
  echo [ERROR] go install failed.
  pause
  exit /b 1
)

set "GOBIN="
for /f "usebackq delims=" %%i in (`go env GOBIN`) do set "GOBIN=%%i"
if defined GOBIN (
  set "HEROS_BIN=%GOBIN%"
) else (
  for /f "usebackq delims=" %%i in (`go env GOPATH`) do set "HEROS_BIN=%%i\bin"
)

if not exist "%HEROS_BIN%\heros-desktop.exe" (
  echo [ERROR] Expected "%HEROS_BIN%\heros-desktop.exe" after install.
  pause
  exit /b 1
)

echo.
echo Adding "%HEROS_BIN%" to user PATH...
"%HEROS_BIN%\heros-desktop.exe" -add-path
if errorlevel 1 (
  echo [ERROR] heros-desktop -add-path failed.
  pause
  exit /b 1
)

echo.
echo === Done ===
echo Open a NEW Command Prompt or PowerShell window, then run:  heros-desktop
echo.
pause
endlocal
