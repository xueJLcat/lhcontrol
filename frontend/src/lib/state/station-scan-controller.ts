import {
  CheckAllStationStatuses,
  GetCurrentStationInfo,
  GetScanStatus,
  IsScanning,
  ScanAndFetchStations,
  StopScan
} from '../backend';
import type { StationInfo } from '../types';
import { classifyScanError, scanErrorCopy, type ScanErrorInfo } from '../scan-error';
import { formatTerminalScanResult, isTerminalScanState } from '../result-format';
import { pushToast } from '../toast';
import type { GlobalOperation } from '../operation-state';
import { ExternalScanCoordinator } from '../external-scan';
import { OperationGate } from '../operation-gate';
import { RevisionGate } from '../revision-gate';
import { t, withDetail } from '../i18n.svelte';

// A stop whose StopScan settled while the backend scan was still finishing has
// nothing else driving it: the next periodic poll may be minutes away with a
// long user-tuned interval. Actively recheck whether the scan ended so the
// header leaves the disabled "Stopping..." state promptly.
const STOP_RECHECK_DELAY_MS = 1500;
// The recheck chain must not run unbounded: if the backend keeps failing the
// probe (or never finishes the scan), give up and let the periodic poll
// reconcile the state instead of pinning the header on "Stopping...".
const STOP_RECHECK_MAX_ATTEMPTS = 10;
// Wails bindings carry no timeout: a BLE scan wedged inside an adapter call
// that ignores cancellation would otherwise keep the UI in "scanning" forever
// (periodic polling is gated by isLoading, and only the scan promise's
// finally clears the state). After this generous window — well past the
// longest configured scan plus the read-phase budgets — the watchdog requests
// a backend stop and reconciles the local state instead of waiting on the
// hung promise indefinitely.
const SCAN_WATCHDOG_DELAY_MS = 90000;
const SCAN_WATCHDOG_RECHECK_DELAY_MS = 5000;
const SCAN_WATCHDOG_MAX_ATTEMPTS = 24;

export interface StationScanHost {
  stations: StationInfo[];
  statusMessage: string;
  globalOperation: GlobalOperation;
  scanError: ScanErrorInfo | null;
  externalScanning: boolean;
  stoppingScan: boolean;
  stopRequestPending: boolean;
  stopRequestGeneration: number;
  startupPending: boolean;
  readonly disposed: boolean;
  readonly autoSleepRunning: boolean;
  readonly externalOperationRunning: boolean;
  readonly isStatusChecking: boolean;
  readonly isLoading: boolean;
  readonly isBulkLoading: boolean;
  readonly anyDeviceOperation: boolean;
  readonly scanningActive: boolean;
  readonly scanLocked: boolean;
  readonly gates: OperationGate;
  readonly listRevisions: RevisionGate;
  readonly externalScan: ExternalScanCoordinator;
  prepareForScan(clearOperations?: boolean): void;
  applyStationList(
    updated: StationInfo[] | null | undefined,
    revision: number,
    capturedStationRevisions?: Map<string, number>
  ): boolean;
  beginScanTimer(): void;
  maybeEndScanTimer(): void;
}

export class StationScanController {
  private stopRecheckTimer: ReturnType<typeof setTimeout> | null = null;
  private scanWatchdogTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(private host: StationScanHost) {}

  dispose() {
    this.cancelStopRecheck();
    this.cancelScanWatchdog();
  }

  private cancelStopRecheck() {
    if (this.stopRecheckTimer !== null) clearTimeout(this.stopRecheckTimer);
    this.stopRecheckTimer = null;
  }

  private cancelScanWatchdog() {
    if (this.scanWatchdogTimer !== null) clearTimeout(this.scanWatchdogTimer);
    this.scanWatchdogTimer = null;
  }

  private armScanWatchdog(operationEpoch: number, statusOperation: number) {
    this.cancelScanWatchdog();
    this.scanWatchdogTimer = setTimeout(() => {
      this.scanWatchdogTimer = null;
      void this.runScanWatchdog(operationEpoch, statusOperation, 0);
    }, SCAN_WATCHDOG_DELAY_MS);
  }

  // Reconciles a local scan that exceeded every plausible backend duration.
  // While the backend still reports scanning, request a stop (unless one is
  // already in flight) and keep rechecking with a bounded budget. Once the
  // backend reports no scan — or the budget runs out — force-settle the local
  // scanning state: the hung scan promise's finally becomes a no-op because
  // globalOperation is no longer 'scanning', and any late response is still
  // gated by the scan epoch and list revision. This restores periodic polling
  // and the Scan/Stop controls instead of forcing a process restart.
  private async runScanWatchdog(operationEpoch: number, statusOperation: number, attempt: number) {
    const host = this.host;
    if (host.disposed || host.globalOperation !== 'scanning') return;
    if (!host.gates.canCommitOperation(operationEpoch)) return;
    const scanning = await IsScanning().catch(() => true);
    if (host.disposed || host.globalOperation !== 'scanning') return;
    if (!host.gates.canCommitOperation(operationEpoch)) return;
    if (scanning) {
      if (!host.stoppingScan && !host.stopRequestPending) {
        void StopScan().catch(() => {
          // A failed stop does not abort the reconciliation chain; the next
          // attempt retries the request.
        });
      }
      if (attempt + 1 < SCAN_WATCHDOG_MAX_ATTEMPTS) {
        this.scanWatchdogTimer = setTimeout(() => {
          this.scanWatchdogTimer = null;
          void this.runScanWatchdog(operationEpoch, statusOperation, attempt + 1);
        }, SCAN_WATCHDOG_RECHECK_DELAY_MS);
        return;
      }
    }
    host.globalOperation = 'idle';
    host.stoppingScan = false;
    const summary = await this.completedScanSummary();
    // Claim the status line before writing: while the probe was awaiting the
    // backend an auto-sleep event can take the line (its message must win),
    // and the hung scan promise settling late must not overwrite this outcome
    // with its own status write.
    if (!host.disposed && host.gates.canCommitStatus(statusOperation)) {
      host.gates.beginStatusOperation();
      host.statusMessage = summary ?? t('Scan stopped.');
    }
    host.maybeEndScanTimer();
  }

  // The backend accepted the stop but the scan had not finished when finishStop
  // checked; schedule a short recheck chain until the terminal outcome lands or
  // the stop request is superseded. Without this, only the periodic status poll
  // (up to a user-tuned minutes-long interval) would clear the stopping state.
  private scheduleStopRecheck(
    operationEpoch: number,
    statusOperation: number,
    requestGeneration: number,
    attempt: number
  ) {
    this.cancelStopRecheck();
    if (this.host.disposed) return;
    if (attempt >= STOP_RECHECK_MAX_ATTEMPTS) {
      // Restore the header; the periodic poll reconciles the real scan state
      // (and re-arms the stop if the scan is genuinely still running).
      this.host.stoppingScan = false;
      return;
    }
    this.stopRecheckTimer = setTimeout(() => {
      this.stopRecheckTimer = null;
      void this.retryStopFinish(operationEpoch, statusOperation, requestGeneration, attempt);
    }, STOP_RECHECK_DELAY_MS);
  }

  private async retryStopFinish(
    operationEpoch: number,
    statusOperation: number,
    requestGeneration: number,
    attempt: number
  ) {
    const host = this.host;
    if (host.disposed || !host.stoppingScan || host.stopRequestGeneration !== requestGeneration) return;
    if (!host.gates.canCommitOperation(operationEpoch)) return;
    if (!host.externalScanning) {
      await this.finishLocalStopRecheck(operationEpoch, statusOperation, requestGeneration, attempt);
      return;
    }
    const outcome = await host.externalScan.finishStop(
      operationEpoch,
      statusOperation,
      () => host.stopRequestGeneration === requestGeneration
    );
    if (outcome === 'still-scanning') this.scheduleStopRecheck(operationEpoch, statusOperation, requestGeneration, attempt + 1);
  }

  // A local scan's promise normally clears stoppingScan in its finally block.
  // When StopScan timed out, that promise can stay pending behind the same
  // stuck backend call, leaving the header pinned on "Stopping...". Probe
  // the authoritative backend state and settle the stop once the scan is gone;
  // the bounded chain gives up (restoring the header) when the backend stays
  // wedged, matching the external-scan behavior.
  private async finishLocalStopRecheck(
    operationEpoch: number,
    statusOperation: number,
    requestGeneration: number,
    attempt: number
  ) {
    const host = this.host;
    const scanning = await IsScanning().catch(() => true);
    if (host.disposed || !host.stoppingScan || host.stopRequestGeneration !== requestGeneration) return;
    if (!host.gates.canCommitOperation(operationEpoch)) return;
    if (scanning && host.globalOperation === 'scanning') {
      this.scheduleStopRecheck(operationEpoch, statusOperation, requestGeneration, attempt + 1);
      return;
    }
    host.stoppingScan = false;
    if (host.gates.canCommitStatus(statusOperation)) {
      const summary = await this.completedScanSummary();
      if (!host.disposed && host.gates.canCommitStatus(statusOperation)) {
        host.statusMessage = summary ?? t('Scan stopped.');
      }
    }
  }

  // Settles the status line on an early return when this scan still owns it.
  // A superseding operation can take the station list or the scan epoch while
  // no newer status owner exists; without this, "Scanning for base stations..."
  // stays in the footer until the next message-writing action. Prefer the
  // backend's real terminal outcome; when the scan is still running (another
  // owner took over the adapter) report the takeover as a stopped scan.
  private async settleSupersededScanStatus(statusOperation: number) {
    if (this.host.disposed || !this.host.gates.canCommitStatus(statusOperation)) return;
    const scanStatus = await GetScanStatus().catch(() => null);
    if (this.host.disposed || !this.host.gates.canCommitStatus(statusOperation)) return;
    if (scanStatus && isTerminalScanState(scanStatus.state)) {
      this.host.statusMessage = formatTerminalScanResult({
        state: scanStatus.state,
        found: scanStatus.found ?? this.host.stations.filter((station) => station.seenInLatestScan).length,
        known: this.host.stations.length,
        error: scanStatus.error,
        warnings: scanStatus.warnings
      });
      return;
    }
    this.host.statusMessage = t('Scan stopped.');
  }

  // Reads the backend scan status and renders its completion summary when the
  // scan finished on its own. Returns null for any other state so callers can
  // fall back to their own message.
  private async completedScanSummary(): Promise<string | null> {
    const scanStatus = await GetScanStatus().catch(() => null);
    if (!scanStatus || scanStatus.state !== 'completed') return null;
    return formatTerminalScanResult({
      state: 'completed',
      found: scanStatus.found ?? this.host.stations.filter((station) => station.seenInLatestScan).length,
      known: this.host.stations.length,
      warnings: scanStatus.warnings
    });
  }

  async periodicStatusCheck() {
    // External HTTP operations hold the backend's global operation lock, so a
    // status poll started in that window can only fail with "operation in
    // progress" and would overwrite the status line with a spurious error.
    if (this.host.startupPending || this.host.autoSleepRunning || this.host.externalOperationRunning || this.host.isStatusChecking || this.host.isLoading || this.host.isBulkLoading || this.host.anyDeviceOperation) return;
    this.host.globalOperation = 'status-refresh';
    const statusOperation = this.host.gates.currentStatusEpoch;
    const revision = this.host.listRevisions.next();
    const capturedStationRevisions = this.host.gates.snapshotStationRevisions();
    try {
      const scanning = await IsScanning();
      if (this.host.disposed || !this.host.listRevisions.isCurrent(revision)) return;
      // Auto-sleep runs its own scan. Adopting it as an external scan would
      // offer Stop for an internal operation (cancelling the pending sleep)
      // and replay a bogus recovery once it ends; skip this tick entirely.
      // Recheck every foreground owner after the asynchronous IsScanning call:
      // one can start while that query is pending even though the entry guard
      // observed an idle host.
      if (this.host.autoSleepRunning || this.host.externalOperationRunning ||
        this.host.isLoading || this.host.isBulkLoading || this.host.anyDeviceOperation) return;
      const wasExternalScanning = this.host.externalScanning;
      if (scanning && !this.host.isLoading && !this.host.externalScanning) {
        await this.host.externalScan.adoptUnknown();
        return;
      }
      this.host.externalScanning = scanning && !this.host.isLoading;
      if (!scanning) {
        this.host.stoppingScan = false;
        if (wasExternalScanning) {
          this.host.externalScan.markRecoveryPending();
        }
        if (this.host.externalScan.hasPendingRecovery()) {
          await this.host.externalScan.recoverTrackedTerminal(revision, statusOperation, capturedStationRevisions);
        } else if (this.host.externalScan.hasPendingTerminal()) {
          // A scan ended while no listener tracked it. Apply its terminal
          // outcome now instead of waiting for the next external scan event.
          await this.host.externalScan.recoverUntracked();
          return;
        } else if (!this.host.applyStationList(await CheckAllStationStatuses(), revision, capturedStationRevisions)) return;
        this.host.maybeEndScanTimer();
      } else {
        this.host.beginScanTimer();
        if (this.host.externalScanning) {
          const scanStatus = await GetScanStatus().catch(() => null);
          // A pending stop owns the status message; letting the poll
          // overwrite "Stopping scan..." contradicts the header button. The
          // status-epoch check stops this late-arriving scan message from
          // clobbering a newer status written meanwhile (for example an
          // auto-sleep skipped/failed event).
          if (!this.host.disposed && this.host.listRevisions.isCurrent(revision) && scanStatus &&
            this.host.gates.canCommitStatus(statusOperation) &&
            !this.host.stoppingScan && !this.host.stopRequestPending) {
            this.host.statusMessage = scanStatus.state === 'starting'
              ? t('Preparing external scan...')
              : t('External scan in progress...');
          }
        }
      }
    } catch (error) {
      if (this.host.disposed || !this.host.listRevisions.isCurrent(revision)) return;
      console.error('Periodic status check failed:', error);
      const fallback = await GetCurrentStationInfo().catch(() => this.host.stations);
      if (!this.host.applyStationList(fallback, revision, capturedStationRevisions)) return;
      // While the scan recovery card is up the Bluetooth outage is already
      // explained; repeating this failure in the status line every poll
      // cycle only re-surfaces the same error.
      if (this.host.gates.canCommitStatus(statusOperation) && !this.host.scanError) this.host.statusMessage = withDetail('Status refresh incomplete', error);
    } finally {
      if (!this.host.disposed && this.host.globalOperation === 'status-refresh') this.host.globalOperation = 'idle';
    }
  }

  // Returns whether a scan actually started. It reports false when a lock
  // (another scan, a bulk operation, or an external/auto-sleep action) keeps
  // the scan from starting, so callers such as the startup flow can defer and
  // retry instead of silently giving up.
  async startScan(): Promise<boolean> {
    if (this.host.isLoading || this.host.scanLocked) return false;
    this.host.prepareForScan();
    // A pending stop belongs to the superseded scan. Its StopScan promise
    // can still be settling while the empty fleet lets the user start a new
    // scan; the new scan must show its own running state and a working Stop.
    // The old stop's finally never clears stoppingScan while a scan runs,
    // so this reset cannot be clobbered.
    this.host.stoppingScan = false;
    this.host.externalScan.resetForLocalScan();
    this.host.globalOperation = 'scanning';
    const statusOperation = this.host.gates.beginStatusOperation();
    this.host.beginScanTimer();
    const operationEpoch = this.host.gates.beginScanEpoch();
    this.armScanWatchdog(operationEpoch, statusOperation);
    const revision = this.host.listRevisions.next();
    this.host.statusMessage = t('Scanning for base stations...');
    try {
      if (!this.host.applyStationList(await ScanAndFetchStations(), revision)) {
        // The list was superseded mid-flight. Settle the status line when this
        // scan still owns it, or "Scanning for base stations..." stays in the
        // footer until the next message-writing action.
        await this.settleSupersededScanStatus(statusOperation);
        return true;
      }
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision) || !this.host.gates.canCommitStatus(statusOperation)) {
        await this.settleSupersededScanStatus(statusOperation);
        return true;
      }
      this.host.scanError = null;
      // A non-terminal status means another scan started while this read was
      // in flight; its own events own the status line from here on.
      if (scanStatus && !isTerminalScanState(scanStatus.state)) return true;
      const found = scanStatus?.found ?? this.host.stations.filter((station) => station.seenInLatestScan).length;
      this.host.statusMessage = formatTerminalScanResult({
        state: scanStatus?.state ?? 'completed',
        found, known: this.host.stations.length,
        error: scanStatus?.error,
        warnings: scanStatus?.warnings
      });
    } catch (error) {
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision) || !this.host.gates.canCommitStatus(statusOperation)) {
        await this.settleSupersededScanStatus(statusOperation);
        return true;
      }
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision)) {
        await this.settleSupersededScanStatus(statusOperation);
        return true;
      }
      if (updated) this.host.applyStationList(updated, revision);
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision) || !this.host.gates.canCommitStatus(statusOperation)) {
        await this.settleSupersededScanStatus(statusOperation);
        return true;
      }
      // A scan that ended while a newer scan is already running was stopped
      // (superseded); only a terminal status can tell failed from stopped.
      const supersededByNewScan = scanStatus !== null && !isTerminalScanState(scanStatus.state);
      if (this.host.stoppingScan || supersededByNewScan || scanStatus?.state === 'cancelled') {
        if (!this.host.stopRequestPending) this.host.stoppingScan = false;
        this.host.statusMessage = t('Scan stopped.');
        this.host.scanError = null;
      } else {
        // The persistent recovery card carries the heading, guidance and raw
        // detail; a toast plus a verbatim status line were two extra copies
        // of the same failure. The status line keeps only a short summary.
        const classified = classifyScanError(error);
        this.host.scanError = classified;
        this.host.statusMessage = classified.kind === 'unknown'
          ? t('Scan failed.')
          : t('Scan failed: {heading}', { heading: scanErrorCopy(classified).heading });
      }
    } finally {
      this.cancelScanWatchdog();
      // The watchdog can force-settle this scan when the backend hangs, and
      // a newer scan may start before this late promise resolves. The epoch
      // check keeps the settled scan's cleanup from clearing a newer owner's
      // scanning state.
      if (!this.host.disposed && this.host.gates.canCommitOperation(operationEpoch)) {
        if (this.host.globalOperation === 'scanning') this.host.globalOperation = 'idle';
        if (!this.host.stopRequestPending && !this.host.externalScanning) this.host.stoppingScan = false;
      }
      this.host.maybeEndScanTimer();
    }
    return true;
  }

  async stopScan() {
    if (!this.host.scanningActive || this.host.stoppingScan) return;
    const operationEpoch = this.host.gates.currentScanEpoch;
    // Own the status line for the stop so the terminal message can be gated
    // against newer owners (an auto-sleep or HTTP event that lands while
    // StopScan is in flight must not be clobbered by this late write).
    const statusOperation = this.host.gates.beginStatusOperation();
    const requestGeneration = ++this.host.stopRequestGeneration;
    this.host.stoppingScan = true;
    this.host.stopRequestPending = true;
    this.host.statusMessage = t('Stopping scan...');
    try {
      await StopScan();
      if (!this.host.gates.canCommitOperation(operationEpoch)) return;
      this.host.stopRequestPending = false;
      if (!this.host.stoppingScan) return;
      if (this.host.externalScanning) {
        const outcome = await this.host.externalScan.finishStop(
          operationEpoch,
          statusOperation,
          () => this.host.stopRequestGeneration === requestGeneration
        );
        if (outcome === 'still-scanning') this.scheduleStopRecheck(operationEpoch, statusOperation, requestGeneration, 1);
      } else {
        if (this.host.globalOperation !== 'scanning') this.host.stoppingScan = false;
        if (this.host.gates.canCommitStatus(statusOperation)) {
          // The scan can complete on its own while the stop is in flight;
          // report its real outcome instead of a plain "stopped" message.
          const summary = await this.completedScanSummary();
          if (this.host.gates.canCommitStatus(statusOperation)) {
            this.host.statusMessage = summary ?? t('Scan stopped.');
          }
        }
        this.host.maybeEndScanTimer();
      }
    } catch (error) {
      if (!this.host.gates.canCommitOperation(operationEpoch)) return;
      this.host.stopRequestPending = false;
      // A scan that finished while the stop request failed keeps its
      // completion summary instead of a misleading stop-failure toast.
      const summary = await this.completedScanSummary();
      if (summary !== null && this.host.gates.canCommitStatus(statusOperation)) {
        this.host.stoppingScan = false;
        this.host.statusMessage = summary;
        return;
      }
      // A stop-timeout means the cancellation was accepted but scan processing
      // had not drained within the bounded wait; keep the stopping state and
      // let the recheck chain or the periodic poll observe the terminal
      // outcome instead of reporting a hard stop failure. Local scans need the
      // chain too: when the scan promise itself stays pending behind the stuck
      // backend call, nothing else clears the "Stopping..." header, and the
      // periodic poll is blocked by isLoading.
      if (classifyScanError(error).kind === 'timeout') {
        if (this.host.gates.canCommitStatus(statusOperation)) {
          this.host.statusMessage = t('Stopping scan...');
        }
        this.scheduleStopRecheck(operationEpoch, statusOperation, requestGeneration, 1);
        return;
      }
      this.host.stoppingScan = false;
      const failureMessage = withDetail('Unable to stop scan', error);
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = failureMessage;
      }
      // As with other action failures, the toast must not be gated by
      // status-line ownership.
      pushToast(failureMessage);
    } finally {
      // Terminal scan events can advance scanEpoch before StopScan settles.
      // Clear this request by identity rather than treating it as stale.
      if (this.host.stopRequestGeneration === requestGeneration) {
        this.host.stopRequestPending = false;
        if (this.host.globalOperation !== 'scanning' && !this.host.externalScanning) this.host.stoppingScan = false;
      }
    }
  }
}
