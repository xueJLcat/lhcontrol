#!/bin/bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$repository_root/frontend"
npm ci
npm run check
npm test
npm run build

cd "$repository_root"
go test ./...
# The bundled Bluetooth fork lives in a separate module behind a replace
# directive, so `go test ./...` never reaches it. Run its own test suite
# explicitly: it carries the WinRT stability patches this build depends on.
go test tinygo.org/x/bluetooth/...
