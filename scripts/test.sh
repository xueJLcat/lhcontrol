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
