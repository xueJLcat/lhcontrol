param(
    [string]$ApiBase = "http://127.0.0.1:7575",
    [ValidateRange(1, 100)]
    [int]$ScanCycles = 10,
    [switch]$ExercisePower
)

$ErrorActionPreference = "Stop"
$results = [ordered]@{
    startedAt = (Get-Date).ToString("o")
    scans = @()
    power = @()
}

function Wait-Scan {
    $deadline = (Get-Date).AddSeconds(60)
    while ((Get-Date) -lt $deadline) {
        $status = Invoke-RestMethod -Method Get -Uri "$ApiBase/scan/status"
        if ($status.state -eq "completed") {
            return $status
        }
        if ($status.state -eq "failed") {
            throw "Scan failed: $($status.error)"
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Scan did not complete within 60 seconds"
}

Invoke-RestMethod -Method Get -Uri "$ApiBase/health" | Out-Null
for ($cycle = 1; $cycle -le $ScanCycles; $cycle++) {
    Invoke-RestMethod -Method Post -Uri "$ApiBase/scan" | Out-Null
    $scan = Wait-Scan
    $stations = @(Invoke-RestMethod -Method Get -Uri "$ApiBase/status")
    $results.scans += [ordered]@{
        cycle = $cycle
        found = $scan.found
        warnings = @($scan.warnings)
        stations = $stations
    }
    Write-Host "Scan $cycle/$ScanCycles completed: $($scan.found) found, $($stations.Count) known"
}

if ($ExercisePower) {
    foreach ($target in @("on", "standby", "sleep")) {
        $body = @{ state = $target } | ConvertTo-Json
        $result = Invoke-RestMethod -Method Post -Uri "$ApiBase/stations/power" -ContentType "application/json" -Body $body
        $results.power += $result
        Write-Host "Bulk $target completed: $(@($result.results).Count) result(s)"
    }
}

$results.completedAt = (Get-Date).ToString("o")
$outputDirectory = Join-Path $PSScriptRoot "..\build\verification"
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
$outputPath = Join-Path $outputDirectory "hardware-smoke-$(Get-Date -Format 'yyyyMMdd-HHmmss').json"
$results | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $outputPath -Encoding utf8
Write-Host "Verification report: $((Resolve-Path -LiteralPath $outputPath).Path)"
