$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot

# Windows PowerShell 5.1 routes a native command's stderr into the error
# stream, and the script-wide $ErrorActionPreference=Stop turns every stderr
# line into a terminating NativeCommandError even when the command succeeds
# (vitest writes test progress to stderr). Relax the preference around the
# child process so its output streams normally, then enforce failure through
# the real exit code instead of the error stream.
# ($PSNativeCommandUseErrorActionPreference does not help: it exists only on
# PowerShell 7.3+, and running through cmd.exe alone does not stop PowerShell
# from classifying the relayed stderr.)
function Invoke-Native {
    param([Parameter(Mandatory)][string]$Command)
    $previous = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        cmd /c "$Command"
    }
    finally {
        $ErrorActionPreference = $previous
    }
    if ($LASTEXITCODE -ne 0) { throw "$Command failed with exit code $LASTEXITCODE" }
}

Push-Location (Join-Path $repositoryRoot "frontend")
try {
    Invoke-Native "npm ci"
    Invoke-Native "npm run check"
    Invoke-Native "npm test"
    Invoke-Native "npm run build"
}
finally {
    Pop-Location
}

Push-Location $repositoryRoot
try {
    Invoke-Native "go test ./..."
    # The bundled Bluetooth fork lives in a separate module behind a replace
    # directive, so `go test ./...` never reaches it. Run its own test suite
    # explicitly: it carries the WinRT stability patches this build depends on.
    Invoke-Native "go test tinygo.org/x/bluetooth/..."
}
finally {
    Pop-Location
}
