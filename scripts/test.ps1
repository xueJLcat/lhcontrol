$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $repositoryRoot "frontend")
try {
    npm ci
    if ($LASTEXITCODE -ne 0) { throw "npm ci failed with exit code $LASTEXITCODE" }
    npm run check
    if ($LASTEXITCODE -ne 0) { throw "frontend check failed with exit code $LASTEXITCODE" }
    npm test
    if ($LASTEXITCODE -ne 0) { throw "frontend tests failed with exit code $LASTEXITCODE" }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "frontend build failed with exit code $LASTEXITCODE" }
}
finally {
    Pop-Location
}

Push-Location $repositoryRoot
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "Go tests failed with exit code $LASTEXITCODE" }
}
finally {
    Pop-Location
}
