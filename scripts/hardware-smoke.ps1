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

function Set-ScanRecordSnapshot {
    param($Record, $Scan, [object[]]$Stations)
    $visible = @(Get-VisibleStations $Stations)
    $Record.status = $Scan
    $Record.state = [string]$Scan.state
    $Record.found = [int]$Scan.found
    $Record.warnings = @($Scan.warnings)
    $Record.addresses = @($visible | ForEach-Object { $_.address })
    $Record.stations = @($Stations)
}

function Set-ScanTimeoutEvidence {
    param($Record, $Evidence)
    if ($null -eq $Evidence) {
        return
    }
    if ($null -ne $Evidence.status) {
        Set-ScanRecordSnapshot $Record $Evidence.status $Evidence.stations
    }
    else {
        $Record.stations = @($Evidence.stations)
        $Record.addresses = @((Get-VisibleStations $Evidence.stations) | ForEach-Object { $_.address })
    }
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

function Test-ScanTerminalState {
    param([string]$State)
    return $State -in @("completed", "failed", "cancelled")
}

function Test-RestoreSucceeded {
    param($Result, [object[]]$Stations, [string]$Address, [string]$Target)
    if ($null -eq $Result -or -not [bool]$Result.confirmed) {
        return $false
    }
    $restoredStation = @($Stations | Where-Object { $_.address -eq $Address })
    return $restoredStation.Count -eq 1 -and
        [bool]$restoredStation[0].powerFresh -and
        [bool]$restoredStation[0].powerStateConfirmed -and
        ([string]$restoredStation[0].powerStateName).ToLowerInvariant() -eq $Target
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

    $failedScanRecord = [ordered]@{
        state = "running"; found = 0; warnings = @(); addresses = @(); stations = @(); status = $null
    }
    $failedScan = [pscustomobject]@{ state = "failed"; found = 1; warnings = @("fixture warning"); error = "fixture scan failure" }
    Set-ScanRecordSnapshot $failedScanRecord $failedScan $visible
    try {
        throw "Scan failed: $($failedScan.error)"
    }
    catch {
        $failedScanRecord.error = $_.Exception.Message
    }
    if ($failedScanRecord.state -ne "failed" -or $failedScanRecord.found -ne 1 -or
        $failedScanRecord.addresses.Count -ne 1 -or $null -eq $failedScanRecord.status) {
        throw "Self-test failed: failed scan evidence was not retained"
    }
    $timeoutRecord = [ordered]@{
        state = "running"; found = 0; warnings = @(); addresses = @(); stations = @(); status = $null
    }
    Set-ScanTimeoutEvidence $timeoutRecord ([ordered]@{
        status = [pscustomobject]@{ state = "running"; found = 1; warnings = @("still scanning") }
        stations = $visible
    })
    if ($timeoutRecord.state -ne "running" -or $timeoutRecord.found -ne 1 -or
        $timeoutRecord.addresses.Count -ne 1 -or $null -eq $timeoutRecord.status) {
        throw "Self-test failed: timed-out scan evidence was not retained"
    }

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
    $requestFailureRecord = [ordered]@{
        target = "sleep"; result = $null; requestError = "fixture request failure"; validationError = ""
    }
    if ([string]::IsNullOrWhiteSpace($requestFailureRecord.requestError) -or $null -ne $requestFailureRecord.result) {
        throw "Self-test failed: failed bulk request evidence was not retained"
    }
    if ((Get-VerificationExitCode $true $true) -ne 1) {
        throw "Self-test failed: a restore failure did not produce a failing exit code"
    }
    foreach ($terminalState in @("completed", "failed", "cancelled")) {
        if (-not (Test-ScanTerminalState $terminalState)) {
            throw "Self-test failed: $terminalState was not recognized as a terminal scan state"
        }
    }
    if (Test-ScanTerminalState "running") {
        throw "Self-test failed: a running scan was recognized as terminal"
    }
    $confirmedReadback = @([pscustomobject]@{
        address = "AA"; powerFresh = $true; powerStateConfirmed = $true; powerStateName = "sleep"
    })
    if (-not (Test-RestoreSucceeded ([pscustomobject]@{
        commandSent = $false; confirmed = $true
    }) $confirmedReadback "AA" "sleep")) {
        throw "Self-test failed: a confirmed no-op restore was rejected"
    }
    if (Test-RestoreSucceeded ([pscustomobject]@{
        commandSent = $true; confirmed = $false
    }) $confirmedReadback "AA" "sleep") {
        throw "Self-test failed: an unconfirmed restore was accepted"
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
$lastScanTimeoutEvidence = $null

function Wait-Scan {
    $deadline = (Get-Date).AddSeconds(60)
    $lastStatus = $null
    while ((Get-Date) -lt $deadline) {
        $status = Invoke-RestMethod -Method Get -Uri "$ApiBase/scan/status"
        $lastStatus = $status
        if (Test-ScanTerminalState ([string]$status.state)) {
            return $status
        }
        Start-Sleep -Milliseconds 500
    }
    $lastStations = @()
    try {
        $lastStations = Get-Stations
    }
    catch {
        $lastStations = @()
    }
    $script:lastScanTimeoutEvidence = [ordered]@{
        status = $lastStatus
        stations = @($lastStations)
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
            status = $null
            found = 0
            warnings = @()
            addresses = @()
            stations = @()
        }
        try {
            Invoke-RestMethod -Method Post -Uri "$ApiBase/scan" | Out-Null
            $scan = Wait-Scan
            $stations = Get-Stations
            Set-ScanRecordSnapshot $scanRecord $scan $stations
            if ($scan.state -eq "failed") {
                throw "Scan failed: $($scan.error)"
            }
            if ($scan.state -eq "cancelled") {
                throw "Scan was cancelled"
            }
            Assert-ScanSnapshot $scan $stations $MinimumStations $ExpectedAddresses
            Write-Host "Scan $cycle/$ScanCycles completed: $($scan.found) found, $($stations.Count) known"
        }
        catch {
            if ($null -ne $lastScanTimeoutEvidence) {
                Set-ScanTimeoutEvidence $scanRecord $lastScanTimeoutEvidence
                $lastScanTimeoutEvidence = $null
            }
            if ($scanRecord.state -ne "cancelled") {
                $scanRecord.state = "failed"
            }
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
            $powerStartedAt = Get-Date
            $powerRecord = [ordered]@{
                target = $target
                durationMs = 0
                result = $null
                readback = $null
                requestError = ""
                validationError = ""
            }
            $results.power += $powerRecord
            $phase = "request"
            try {
                $result = Invoke-RestMethod -Method Post -Uri "$ApiBase/stations/power" -ContentType "application/json" -Body $body
                $powerRecord.result = $result
                $phase = "validation"
                Assert-BulkResult $result $target $snapshotAddresses
                $readback = Get-Stations
                $powerRecord.readback = $readback
                Assert-PowerReadback $result $readback $target
            }
            catch {
                if ($phase -eq "request") {
                    $powerRecord.requestError = $_.Exception.Message
                }
                else {
                    $powerRecord.validationError = $_.Exception.Message
                }
                throw
            }
            finally {
                $powerRecord.durationMs = [math]::Round(((Get-Date) - $powerStartedAt).TotalMilliseconds)
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
                $restoreSucceeded = Test-RestoreSucceeded $restoreResult $readback $entry.Key $entry.Value
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
    if ($restoreFailed -and [string]::IsNullOrWhiteSpace([string]$results.error)) {
        $results.error = "One or more stations could not be restored to their initial power state"
    }
    $results.completedAt = (Get-Date).ToString("o")
    $results | ConvertTo-Json -Depth 16 | Set-Content -LiteralPath $outputPath -Encoding utf8
    Write-Host "Verification report: $((Resolve-Path -LiteralPath $outputPath).Path)"
}

if ((Get-VerificationExitCode $results.succeeded $restoreFailed) -ne 0) {
    throw "Hardware verification failed; see $outputPath"
}
