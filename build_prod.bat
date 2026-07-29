@echo off
setlocal

rem Add GOPATH\bin to PATH so the wails CLI is found
for /f "delims=" %%i in ('go env GOPATH') do set "GOPATH=%%i"
set "PATH=%PATH%;%GOPATH%\bin"

echo Building for Windows with stripped symbols and trimpath...

rem Build for Windows
rem -trimpath: removes file system paths
rem -ldflags "-s -w": strips debug symbols
wails build -platform windows/amd64 -trimpath -ldflags "-s -w" -o lhcontrol.exe
if errorlevel 1 (
    echo Build failed.
    exit /b 1
)
if not exist "build\bin\lhcontrol.exe" (
    echo Build failed: build\bin\lhcontrol.exe is missing.
    exit /b 1
)
for %%A in ("build\bin\lhcontrol.exe") do if %%~zA LEQ 0 (
    echo Build failed: build\bin\lhcontrol.exe is empty.
    exit /b 1
)

echo Build complete. Check build/bin/lhcontrol.exe
endlocal
