param(
    [string]$ApiBase = "http://127.0.0.1:7575",
    [ValidateRange(1, 100)]
    [int]$ScanCycles = 10,
    [ValidateRange(0, 100)]
    [int]$MinimumStations = 1,
    [string[]]$ExpectedAddresses = @(),
    [switch]$ExercisePower,
    [switch]$SelfTest,
    # A scan can legitimately take far longer than a minute: per-station
    # connection release before scanning, the configured scan duration, and
    # the initial-read phase all add up. Keep the wait budget configurable
    # instead of failing slow-but-successful scans.
    [ValidateRange(10, 3600)]
    [int]$ScanWaitSeconds = 300
)

$ErrorActionPreference = "Stop"

# A trailing slash would produce double-slash endpoint URLs that the router
# rejects with a misleading 404.
$ApiBase = $ApiBase.TrimEnd('/')

function Get-HttpErrorStatusCode {
    param($ErrorRecord)
    $response = $ErrorRecord.Exception.Response
    if ($null -eq $response) { return $null }
    return [int]$response.StatusCode
}

# Windows PowerShell 5.1 surfaces non-2xx responses as a WebException whose
# Message only carries "(409) Conflict". The API returns a JSON error body
# with the actual cause; read it so reports keep the server-side detail.
function Get-HttpErrorDetail {
    param($ErrorRecord)
    $message = $ErrorRecord.Exception.Message
    try {
        $response = $ErrorRecord.Exception.Response
        if ($null -ne $response) {
            $stream = $response.GetResponseStream()
            $reader = New-Object System.IO.StreamReader($stream)
            $body = $reader.ReadToEnd()
            $reader.Close()
            if (-not [string]::IsNullOrWhiteSpace($body)) {
                $parsed = $null
                try { $parsed = $body | ConvertFrom-Json } catch { $parsed = $null }
                if ($null -ne $parsed -and $parsed.error) {
                    return "$message -- $($parsed.error)"
                }
                return "$message -- $body"
            }
        }
    }
    catch {
        # Error-body extraction is best-effort; the exception message alone
        # still describes the failure.
    }
    return $message
}

function Get-ChannelSortValue {
    param($Station)
    $channel = [int]$Station.channel
    if ($channel -gt 0) { return $channel }
    return [int]::MaxValue
}

function Compare-StationOrder {
    param($Left, $Right)
    $leftChannel = Get-ChannelSortValue $Left
    $rightChannel = Get-ChannelSortValue $Right
    if ($leftChannel -ne $rightChannel) {
        return $leftChannel - $rightChannel
    }
    $nameComparison = [string]::CompareOrdinal(
        ([string]$Left.name).ToLowerInvariant(),
        ([string]$Right.name).ToLowerInvariant()
    )
    if ($nameComparison -ne 0) { return $nameComparison }
    return [string]::CompareOrdinal(
        ([string]$Left.address).ToLowerInvariant(),
        ([string]$Right.address).ToLowerInvariant()
    )
}

function Assert-StableOrder {
    param([object[]]$Stations)
    # The backend compares the three sort fields fieldwise (channel, then name,
    # then address) with ordinal comparisons on the lowercased values. A single
    # joined sort key misorders prefix-related names (LHB-1 vs LHB-10): the
    # separator would be compared against the next name character, so the check
    # must compare adjacent stations fieldwise to match the backend exactly.
    for ($index = 1; $index -lt $Stations.Count; $index++) {
        if ((Compare-StationOrder $Stations[$index - 1] $Stations[$index]) -gt 0) {
            throw "Station list is not sorted by channel, name, and address"
        }
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
        # A mismatch is legitimate under the application's documented
        # degradation paths (a wedged station lock skipping its presence
        # bookkeeping, a failed pre-scan connection release leaving presence
        # uncertain). Only treat it as a hard failure when the scan reported
        # no warnings at all; otherwise record it and keep the cycle going.
        if (@($Scan.warnings).Count -eq 0) {
            throw "Scan reported $($Scan.found) found station(s), but status contains $($visible.Count) from this scan"
        }
        Write-Warning "Scan found/visible mismatch ($($Scan.found) vs $($visible.Count)) accepted because the scan reported warnings: $($Scan.warnings -join '; ')"
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

# Skip reasons that legitimately keep a station unexercised. Every other
# reason (a wedged lock, a per-station timeout, cancellation, shutdown,
# booting) means the station was never exercised and must fail the run.
$script:BenignSkipReasons = @(
    "already at target state",
    "power control is not supported",
    "standby is not supported"
)

function Assert-BulkResult {
    param($Result, [string]$Target, [string[]]$Expected)
    # A timed-out or cancelled bulk returns 200 with structured partial
    # results; the top-level flags are the only signal that the batch never
    # ran to completion. Checking items alone would accept a bulk that only
    # reached a fraction of the fleet.
    if ([bool]$Result.cancelled -or [bool]$Result.timedOut) {
        throw "Bulk $Target did not run to completion (cancelled=$([bool]$Result.cancelled), timedOut=$([bool]$Result.timedOut))"
    }
    $actualAddresses = @($Result.results | ForEach-Object { ([string]$_.address).ToLowerInvariant() } | Sort-Object)
    $expectedAddresses = @($Expected | ForEach-Object { $_.ToLowerInvariant() } | Sort-Object)
    if (($actualAddresses -join "`n") -ne ($expectedAddresses -join "`n")) {
        throw "Bulk $Target result addresses do not match the pre-operation station snapshot"
    }
    foreach ($item in @($Result.results)) {
        if ($item.skipped) {
            $reason = ([string]$item.reason).ToLowerInvariant()
            if ([string]::IsNullOrWhiteSpace($reason)) {
                throw "Bulk $Target skipped $($item.address) without a reason"
            }
            if ($script:BenignSkipReasons -notcontains $reason) {
                throw "Bulk $Target skipped $($item.name) ($($item.address)) without exercising it: $($item.reason)"
            }
            continue
        }
        if (-not $item.success -or -not $item.commandSent) {
            throw "Bulk $Target failed for $($item.name) ($($item.address)): $($item.error)"
        }
        if ($Target -eq "sleep") {
            # Firmware drops the BLE link as the station powers down, so a
            # successful sleep command reads back as sent-but-unconfirmed
            # (a sleep-transition disconnect). Requiring confirmed here would
            # make the sleep phase impossible to pass on real hardware; the
            # already-asleep no-op is the only confirmed case.
            continue
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
    if ($Target -eq "sleep") {
        # Sleeping stations dropped the BLE link; there is nothing to read
        # back. The bulk result's own per-station outcome (validated by
        # Assert-BulkResult) is authoritative for the sleep phase.
        return
    }
    foreach ($item in @($Result.results)) {
        if ($item.skipped) {
            continue
        }
        $readback = @($Stations | Where-Object { $_.address -eq $item.address })
        if ($readback.Count -ne 1) {
            throw "Bulk $Target readback is missing station $($item.address)"
        }
        # The authoritative confirmation is the bulk result's per-station
        # confirmed flag, computed at confirmation time. A post-bulk status
        # snapshot can already be stale on long batches (the display freshness
        # window is shorter than a multi-station bulk), so cross-check the
        # state name only while the readback is still fresh; a stale readback
        # must not fail a command the device already confirmed.
        if ([bool]$readback[0].powerFresh -and
            ([string]$readback[0].powerStateName).ToLowerInvariant() -ne $Target) {
            throw "Bulk $Target readback shows an unexpected state for $($item.address)"
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
    if ($null -eq $Result) {
        return $false
    }
    if ($Target -eq "sleep") {
        # Restoring sleep re-sends the sleep command; the firmware disconnect
        # makes sent-but-unconfirmed the success shape (and a confirmed no-op
        # when the station was already asleep). A fresh readback of a sleeping
        # station is not obtainable, so it cannot be required.
        return [bool]$Result.commandSent -or [bool]$Result.confirmed
    }
    if (-not [bool]$Result.confirmed) {
        return $false
    }
    $restoredStation = @($Stations | Where-Object { $_.address -eq $Address })
    return $restoredStation.Count -eq 1 -and
        [bool]$restoredStation[0].powerFresh -and
        [bool]$restoredStation[0].powerStateConfirmed -and
        ([string]$restoredStation[0].powerStateName).ToLowerInvariant() -eq $Target
}

function Invoke-SelfTest {
    # The visibility filter must decide this fixture: with a correct filter
    # only AA is visible, so found(2) != visible(1) throws; a broken filter
    # that returns every station would make found and visible agree, satisfy
    # the minimum, and list the expected historical address -- no throw, and
    # the self-test alarm below fires.
    $mixedVisibility = @(
        [pscustomobject]@{ channel = 1; name = "Known"; address = "BB"; seenInLatestScan = $false }
        [pscustomobject]@{ channel = 1; name = "Visible"; address = "AA"; seenInLatestScan = $true }
    )
    $rejectedHistoricalStation = $false
    try {
        Assert-ScanSnapshot ([pscustomobject]@{ found = 2; warnings = @() }) $mixedVisibility 1 @("BB")
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
    Assert-ScanSnapshot ([pscustomobject]@{ found = 1; warnings = @() }) $visible 1 @("AA")

    # Prefix-related names must be ordered fieldwise, not by a joined key:
    # LHB-1 precedes LHB-10 (the shorter prefix first), the exact ordering a
    # single joined sort key would reverse.
    $prefixOrdered = @(
        [pscustomobject]@{ channel = 1; name = "LHB-1"; address = "AA"; seenInLatestScan = $true }
        [pscustomobject]@{ channel = 1; name = "LHB-10"; address = "AB"; seenInLatestScan = $true }
    )
    Assert-StableOrder $prefixOrdered
    $prefixReversed = @($prefixOrdered[1], $prefixOrdered[0])
    $rejectedPrefixOrder = $false
    try {
        Assert-StableOrder $prefixReversed
    }
    catch {
        $rejectedPrefixOrder = $true
    }
    if (-not $rejectedPrefixOrder) {
        throw "Self-test failed: a prefix-misordered station list was accepted"
    }

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
    # A sleep restore succeeds as sent-but-unconfirmed: the firmware drops
    # the link while powering down, so that shape is the expected outcome.
    if (-not (Test-RestoreSucceeded ([pscustomobject]@{
        commandSent = $true; confirmed = $false
    }) $confirmedReadback "AA" "sleep")) {
        throw "Self-test failed: a sleep-transition restore was rejected"
    }
    # The same unconfirmed shape is not a successful restore for any other
    # target; a readback must confirm it.
    if (Test-RestoreSucceeded ([pscustomobject]@{
        commandSent = $true; confirmed = $false
    }) $confirmedReadback "AA" "on") {
        throw "Self-test failed: an unconfirmed non-sleep restore was accepted"
    }
    # A skip whose reason means the station was never exercised must fail the
    # bulk; a benign no-op skip must pass; a timed-out bulk must fail before
    # any per-station inspection.
    $busySkipBulk = [pscustomobject]@{
        cancelled = $false; timedOut = $false
        results = @([pscustomobject]@{
            address = "AA"; name = "Visible"; skipped = $true; reason = "station is busy"
            success = $false; commandSent = $false; confirmed = $false; error = ""
            station = $visible[0]
        })
    }
    $rejectedBusySkip = $false
    try {
        Assert-BulkResult $busySkipBulk "on" @("AA")
    }
    catch {
        $rejectedBusySkip = $true
    }
    if (-not $rejectedBusySkip) {
        throw "Self-test failed: a busy-skipped station was accepted as exercised"
    }
    $noOpSkipBulk = [pscustomobject]@{
        cancelled = $false; timedOut = $false
        results = @([pscustomobject]@{
            address = "AA"; name = "Visible"; skipped = $true; reason = "already at target state"
            success = $true; commandSent = $false; confirmed = $true; error = ""
            station = $visible[0]
        })
    }
    Assert-BulkResult $noOpSkipBulk "on" @("AA")
    $timedOutBulk = [pscustomobject]@{
        cancelled = $false; timedOut = $true
        results = @([pscustomobject]@{
            address = "AA"; name = "Visible"; skipped = $false; reason = ""
            success = $true; commandSent = $true; confirmed = $true; error = ""
            station = $visible[0]
        })
    }
    $rejectedTimedOutBulk = $false
    try {
        Assert-BulkResult $timedOutBulk "on" @("AA")
    }
    catch {
        $rejectedTimedOutBulk = $true
    }
    if (-not $rejectedTimedOutBulk) {
        throw "Self-test failed: a timed-out bulk was accepted"
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
$operatedAddresses = @{}
# Stations skipped for a missing capability in every phase were never altered
# and must not be restored (the restore write would be rejected as
# unsupported). Tracked separately from $operatedAddresses so a lost bulk
# response still restores every station whose capability is known.
$capabilitySkippedAddresses = @{}
$powerStarted = $false
$restoreFailed = $false
$lastScanTimeoutEvidence = $null

function Wait-Scan {
    $deadline = (Get-Date).AddSeconds($ScanWaitSeconds)
    $lastStatus = $null
    $lastPollError = $null
    while ((Get-Date) -lt $deadline) {
        # Bound each poll request itself: without an explicit timeout a hung
        # API keeps the final request inside this loop blocked for the 100s
        # default, overshooting the scan wait budget by minutes. A single
        # failed poll (a connection reset, the app restarting its listener, or
        # one slow response) must not abort the whole wait budget either:
        # remember it and keep polling until the deadline.
        try {
            $status = Invoke-RestMethod -Method Get -Uri "$ApiBase/scan/status" -TimeoutSec 15
            $lastStatus = $status
            $lastPollError = $null
            if (Test-ScanTerminalState ([string]$status.state)) {
                return $status
            }
        }
        catch {
            $lastPollError = $_.Exception.Message
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
    if ($null -eq $lastStatus -and $null -ne $lastPollError) {
        throw "Scan did not complete within $ScanWaitSeconds seconds (status polling failed: $lastPollError)"
    }
    throw "Scan did not complete within $ScanWaitSeconds seconds"
}

function Get-Stations {
    return @(Invoke-RestMethod -Method Get -Uri "$ApiBase/status")
}

function Start-ScanWithRetry {
    param([int]$Attempts = 3)
    # POST /scan returns 409 whenever another Bluetooth operation is active
    # (an auto-sleep action, a UI operation, or a previous scan still
    # draining). A single transient conflict must not fail the whole
    # multi-cycle run: back off briefly and retry; other errors surface
    # immediately.
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            Invoke-RestMethod -Method Post -Uri "$ApiBase/scan" | Out-Null
            return
        }
        catch {
            $statusCode = Get-HttpErrorStatusCode $_
            if ($statusCode -eq 409 -and $attempt -lt $Attempts) {
                Write-Warning "POST /scan conflicted (another Bluetooth operation is active); retry $attempt/$($Attempts - 1) in 3s"
                Start-Sleep -Seconds 3
                continue
            }
            throw
        }
    }
}

function Invoke-StationPower {
    param([string]$Address, [string]$Target)
    $escapedAddress = [uri]::EscapeDataString($Address)
    $body = @{ state = $Target } | ConvertTo-Json
    # A single-station operation may take up to the configured 120s budget;
    # the default 100s REST timeout would abort a legitimately slow command
    # (notably during the restore phase) and misreport the outcome.
    return Invoke-RestMethod -Method Post -Uri "$ApiBase/stations/$escapedAddress/power" -ContentType "application/json" -Body $body -TimeoutSec 180
}

$outputDirectory = Join-Path $PSScriptRoot "..\build\verification"
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
# Millisecond suffix: two runs started in the same second must not overwrite
# each other's report.
$outputPath = Join-Path $outputDirectory "hardware-smoke-$(Get-Date -Format 'yyyyMMdd-HHmmssfff').json"

try {
    $health = Invoke-RestMethod -Method Get -Uri "$ApiBase/health"
    if (-not [bool]$health.running) {
        throw "HTTP API is not running at $ApiBase : $($health.error)"
    }
    if (@($health.warnings).Count -gt 0 -and -not [string]::IsNullOrWhiteSpace("$($health.warnings)")) {
        Write-Warning "HTTP API reports warnings before starting: $($health.warnings -join '; ')"
    }
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
            Start-ScanWithRetry
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
            $scanRecord.error = Get-HttpErrorDetail $_
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
                # A bulk can be configured up to a 600s total timeout; give the
                # client headroom past that instead of aborting a slow batch.
                $result = Invoke-RestMethod -Method Post -Uri "$ApiBase/stations/power" -ContentType "application/json" -Body $body -TimeoutSec 660
                $powerRecord.result = $result
                foreach ($item in @($result.results)) {
                    if ([bool]$item.commandSent) {
                        $operatedAddresses[[string]$item.address] = $true
                    }
                    $skipReason = ([string]$item.reason).ToLowerInvariant()
                    if ([bool]$item.skipped -and
                        $skipReason -in @("power control is not supported", "standby is not supported")) {
                        $capabilitySkippedAddresses[[string]$item.address] = $true
                    }
                }
                $phase = "validation"
                Assert-BulkResult $result $target $snapshotAddresses
                $readback = Get-Stations
                $powerRecord.readback = $readback
                Assert-PowerReadback $result $readback $target
            }
            catch {
                if ($phase -eq "request") {
                    $powerRecord.requestError = Get-HttpErrorDetail $_
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
            if ($capabilitySkippedAddresses.Contains($entry.Key) -and -not $operatedAddresses.Contains($entry.Key)) {
                # A station skipped for a missing capability in every phase
                # was never altered, so there is nothing to restore; the write
                # would be rejected as unsupported and fail the run. Every
                # other station is restored even when a bulk response was
                # lost: the restore write is idempotent (an already-target
                # station returns a confirmed no-op skip).
                continue
            }
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
                $restoreError = Get-HttpErrorDetail $_
                $results.restore += [ordered]@{
                    address = $entry.Key
                    target = $entry.Value
                    succeeded = $false
                    result = $null
                    readback = $null
                    error = $restoreError
                }
                Write-Warning "Failed to restore $($entry.Key): $restoreError"
            }
        }
    }
    if ($restoreFailed -and [string]::IsNullOrWhiteSpace([string]$results.error)) {
        $results.error = "One or more stations could not be restored to their initial power state"
    }
    $results.completedAt = (Get-Date).ToString("o")
    # Write UTF-8 without a BOM: strict JSON parsers reject a leading BOM,
    # and Set-Content -Encoding utf8 emits one on Windows PowerShell 5.1.
    [System.IO.File]::WriteAllText(
        $outputPath,
        ($results | ConvertTo-Json -Depth 16),
        (New-Object System.Text.UTF8Encoding $false)
    )
    Write-Host "Verification report: $((Resolve-Path -LiteralPath $outputPath).Path)"
}

if ((Get-VerificationExitCode $results.succeeded $restoreFailed) -ne 0) {
    throw "Hardware verification failed; see $outputPath"
}
