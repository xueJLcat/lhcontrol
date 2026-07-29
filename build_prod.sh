#!/bin/bash
set -euo pipefail

export PATH=$PATH:$(go env GOPATH)/bin

echo "Building for Windows with stripped symbols and trimpath..."

# Build for Windows
# -trimpath: removes file system paths
# -ldflags "-s -w": strips debug symbols
wails build -platform windows/amd64 -trimpath -ldflags "-s -w" -o lhcontrol.exe

if [[ ! -s build/bin/lhcontrol.exe ]]; then
    echo "Build failed: build/bin/lhcontrol.exe is missing or empty." >&2
    exit 1
fi

echo "Build complete. Check build/bin/lhcontrol.exe"
