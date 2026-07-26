param(
    [string]$ApiBase = "http://127.0.0.1:7575",
    [ValidateRange(1, 100)]
    [int]$ScanCycles = 10,
    [ValidateRange(0, 100)]
    [int]$MinimumStations = 1,
    [string[]]$ExpectedAddresses = @(),
    [switch]$ExercisePower,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Get-SortKey {
    param($Station)
    $channel = if ([int]$Station.channel -gt 0) { [int]$Station.channel } else { [int]::MaxValue }
    return "{0:D10}|{1}|{2}" -f $channel, ([string]$Station.name).ToLowerInvariant(), ([string]$Station.address).ToLowerInvariant()
}

function Assert-StableOrder {
    param([object[]]$Stations)
    $actual = @($Stations | ForEach-Object { Get-SortKey $_ })
    $expected = @($actual | Sort-Object)
    if (($actual -join "`n") -ne ($expected -join "`n")) {
        throw "Station list is not sorted by channel, name, and address"
    }
}

function Get-VisibleStations {
    param([object[]]$Stations)
    return @($Stations | Where-Object { [bool]$_.seenInLatestScan })
}

function Assert-ScanSnapshot {
    param($Scan, [object[]]$Stations, [int]$Minimum, [string[]]$Expected)
    Assert-StableOrder $Stations
    $visible = @(Get-VisibleStations $Stations)
    if ([int]$Scan.found -ne $visible.Count) {
        throw "Scan reported $($Scan.found) found station(s), but status contains $($visible.Count) from this scan"
    }
    if ($visible.Count -lt $Minimum) {
        throw "Scan found $($visible.Count) station(s) in this cycle; expected at least $Minimum"
    }
    $visibleAddresses = @($visible | ForEach-Object { ([string]$_.address).ToLowerInvariant() })
    foreach ($expectedAddress in $Expected) {
        if ($visibleAddresses -notcontains $expectedAddress.ToLowerInvariant()) {
            throw "Scan did not discover expected station $expectedAddress in this cycle"
        }
    }
}

function Assert-BulkResult {
    param($Result, [string]$Target, [string[]]$Expected)
    $actualAddresses = @($Result.results | ForEach-Object { ([string]$_.address).ToLowerInvariant() } | Sort-Object)
    $expectedAddresses = @($Expected | ForEach-Object { $_.ToLowerInvariant() } | Sort-Object)
    if (($actualAddresses -join "`n") -ne ($expectedAddresses -join "`n")) {
        throw "Bulk $Target result addresses do not match the pre-operation station snapshot"
    }
    foreach ($item in @($Result.results)) {
        if ($item.skipped) {
            if ([string]::IsNullOrWhiteSpace([string]$item.reason)) {
                throw "Bulk $Target skipped $($item.address) without a reason"
            }
            continue
        }
        if (-not $item.success -or -not $item.commandSent) {
            throw "Bulk $Target failed for $($item.name) ($($item.address)): $($item.error)"
        }
        if (-not $item.confirmed) {
            throw "Bulk $Target was not confirmed for $($item.name) ($($item.address)): $($item.error)"
        }
        if (([string]$item.station.powerStateName).ToLowerInvariant() -ne $Target) {
            throw "Bulk $Target returned an unexpected state for $($item.name) ($($item.address))"
        }
    }
}

function Assert-PowerReadback {
    param($Result, [object[]]$Stations, [string]$Target)
    foreach ($item in @($Result.results)) {
        if ($item.skipped) {
            continue
        }
        $readback = @($Stations | Where-Object { $_.address -eq $item.address })
        if ($readback.Count -ne 1) {
            throw "Bulk $Target readback is missing station $($item.address)"
        }
        if (-not $readback[0].powerFresh -or -not $readback[0].powerStateConfirmed -or
            ([string]$readback[0].powerStateName).ToLowerInvariant() -ne $Target) {
            throw "Bulk $Target readback was not confirmed for $($item.address)"
        }
    }
}

function Get-VerificationExitCode {
    param([bool]$Succeeded, [bool]$RestoreFailed)
    if ($Succeeded -and -not $RestoreFailed) {
        return 0
    }
    return 1
}

function Invoke-SelfTest {
    $historical = @(
        [pscustomobject]@{ channel = 1; name = "Known"; address = "AA"; seenInLatestScan = $false }
    )
    $rejectedHistoricalStation = $false
    try {
        Assert-ScanSnapshot ([pscustomobject]@{ found = 0 }) $historical 1 @("AA")
    }
    catch {
        $rejectedHistoricalStation = $true
    }
    if (-not $rejectedHistoricalStation) {
        throw "Self-test failed: a historical station satisfied current-scan assertions"
    }

    $visible = @(
        [pscustomobject]@{ channel = 1; name = "Visible"; address = "AA"; seenInLatestScan = $true }
    )
    Assert-ScanSnapshot ([pscustomobject]@{ found = 1 }) $visible 1 @("AA")

    $failedBulk = [pscustomobject]@{
        results = @([pscustomobject]@{
            address = "AA"; name = "Visible"; skipped = $false; reason = ""
            success = $false; commandSent = $false; confirmed = $false; error = "fixture failure"
            station = $visible[0]
        })
    }
    $bulkRecord = [ordered]@{ target = "on"; result = $failedBulk; validationError = "" }
    try {
        Assert-BulkResult $failedBulk "on" @("AA")
    }
    catch {
        $bulkRecord.validationError = $_.Exception.Message
    }
    if ($null -eq $bulkRecord.result -or [string]::IsNullOrWhiteSpace($bulkRecord.validationError)) {
        throw "Self-test failed: failed bulk evidence was not retained"
    }
    if ((Get-VerificationExitCode $true $true) -ne 1) {
        throw "Self-test failed: a restore failure did not produce a failing exit code"
    }
    Write-Host "Hardware smoke self-test passed."
}

if ($SelfTest) {
    Invoke-SelfTest
    return
}

$results = [ordered]@{
    startedAt = (Get-Date).ToString("o")
    succeeded = $false
    error = ""
    scans = @()
    power = @()
    restore = @()
}
$initialStates = [ordered]@{}
$powerStarted = $false
$restoreFailed = $false

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

function Get-Stations {
    return @(Invoke-RestMethod -Method Get -Uri "$ApiBase/status")
}

function Invoke-StationPower {
    param([string]$Address, [string]$Target)
    $escapedAddress = [uri]::EscapeDataString($Address)
    $body = @{ state = $Target } | ConvertTo-Json
    return Invoke-RestMethod -Method Post -Uri "$ApiBase/stations/$escapedAddress/power" -ContentType "application/json" -Body $body
}

$outputDirectory = Join-Path $PSScriptRoot "..\build\verification"
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
$outputPath = Join-Path $outputDirectory "hardware-smoke-$(Get-Date -Format 'yyyyMMdd-HHmmss').json"

try {
    Invoke-RestMethod -Method Get -Uri "$ApiBase/health" | Out-Null
    for ($cycle = 1; $cycle -le $ScanCycles; $cycle++) {
        $started = Get-Date
        $scanRecord = [ordered]@{
            cycle = $cycle
            durationMs = 0
            state = "running"
            error = ""
            found = 0
            warnings = @()
            addresses = @()
            stations = @()
        }
        try {
            Invoke-RestMethod -Method Post -Uri "$ApiBase/scan" | Out-Null
            $scan = Wait-Scan
            $stations = Get-Stations
            Assert-ScanSnapshot $scan $stations $MinimumStations $ExpectedAddresses
            $visible = @(Get-VisibleStations $stations)
            $scanRecord.state = [string]$scan.state
            $scanRecord.found = [int]$scan.found
            $scanRecord.warnings = @($scan.warnings)
            $scanRecord.addresses = @($visible | ForEach-Object { $_.address })
            $scanRecord.stations = $stations
            Write-Host "Scan $cycle/$ScanCycles completed: $($scan.found) found, $($stations.Count) known"
        }
        catch {
            $scanRecord.state = "failed"
            $scanRecord.error = $_.Exception.Message
            throw
        }
        finally {
            $scanRecord.durationMs = [math]::Round(((Get-Date) - $started).TotalMilliseconds)
            $results.scans += $scanRecord
        }
    }

    if ($ExercisePower) {
        $stations = Get-Stations
        $snapshotAddresses = @($stations | ForEach-Object { [string]$_.address })
        foreach ($station in $stations) {
            $state = ([string]$station.powerStateName).ToLowerInvariant()
            if (-not $station.powerFresh -or -not $station.powerStateConfirmed -or
                $state -notin @("on", "standby", "sleep")) {
                throw "Power exercise cancelled before sending commands: $($station.name) has no confirmed restorable state"
            }
            $initialStates[[string]$station.address] = $state
        }
        $powerStarted = $true
        foreach ($target in @("on", "standby", "sleep")) {
            $body = @{ state = $target } | ConvertTo-Json
            $result = Invoke-RestMethod -Method Post -Uri "$ApiBase/stations/power" -ContentType "application/json" -Body $body
            $powerRecord = [ordered]@{
                target = $target
                result = $result
                readback = $null
                validationError = ""
            }
            $results.power += $powerRecord
            try {
                Assert-BulkResult $result $target $snapshotAddresses
                $readback = Get-Stations
                $powerRecord.readback = $readback
                Assert-PowerReadback $result $readback $target
            }
            catch {
                $powerRecord.validationError = $_.Exception.Message
                throw
            }
            Write-Host "Bulk $target completed and validated: $(@($result.results).Count) result(s)"
        }
    }
    $results.succeeded = $true
}
catch {
    $results.error = $_.Exception.Message
    throw
}
finally {
    if ($powerStarted) {
        foreach ($entry in $initialStates.GetEnumerator()) {
            try {
                $restoreResult = Invoke-StationPower -Address $entry.Key -Target $entry.Value
                $readback = Get-Stations
                $restoredStation = @($readback | Where-Object { $_.address -eq $entry.Key })
                $restoreSucceeded = $restoreResult.commandSent -and $restoreResult.confirmed -and
                    $restoredStation.Count -eq 1 -and $restoredStation[0].powerFresh -and
                    $restoredStation[0].powerStateConfirmed -and
                    ([string]$restoredStation[0].powerStateName).ToLowerInvariant() -eq $entry.Value
                $results.restore += [ordered]@{
                    address = $entry.Key
                    target = $entry.Value
                    succeeded = [bool]$restoreSucceeded
                    result = $restoreResult
                    readback = if ($restoredStation.Count -eq 1) { $restoredStation[0] } else { $null }
                    error = ""
                }
                if (-not $restoreSucceeded) {
                    $restoreFailed = $true
                    $results.succeeded = $false
                    Write-Warning "Initial state was not confirmed for $($entry.Key)"
                }
            }
            catch {
                $restoreFailed = $true
                $results.succeeded = $false
                $results.restore += [ordered]@{
                    address = $entry.Key
                    target = $entry.Value
                    succeeded = $false
                    result = $null
                    readback = $null
                    error = $_.Exception.Message
                }
                Write-Warning "Failed to restore $($entry.Key): $($_.Exception.Message)"
            }
        }
    }
    $results.completedAt = (Get-Date).ToString("o")
    $results | ConvertTo-Json -Depth 16 | Set-Content -LiteralPath $outputPath -Encoding utf8
    Write-Host "Verification report: $((Resolve-Path -LiteralPath $outputPath).Path)"
}

if ((Get-VerificationExitCode $results.succeeded $restoreFailed) -ne 0) {
    throw "Hardware verification failed; see $outputPath"
}
