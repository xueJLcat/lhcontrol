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
import { formatTerminalScanResult } from '../result-format';
import { pushToast } from '../toast';
import type { GlobalOperation } from '../operation-state';
import { ExternalScanCoordinator } from '../external-scan';
import { OperationGate } from '../operation-gate';
import { RevisionGate } from '../revision-gate';
import { t, withDetail } from '../i18n.svelte';

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
  constructor(private host: StationScanHost) {}
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

  async startScan() {
    if (this.host.isLoading || this.host.scanLocked) return;
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
    const revision = this.host.listRevisions.next();
    this.host.statusMessage = t('Scanning for base stations...');
    try {
      if (!this.host.applyStationList(await ScanAndFetchStations(), revision)) return;
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision) || !this.host.gates.canCommitStatus(statusOperation)) return;
      const found = scanStatus?.found ?? this.host.stations.filter((station) => station.seenInLatestScan).length;
      this.host.statusMessage = formatTerminalScanResult({
        state: scanStatus?.state ?? 'completed',
        found, known: this.host.stations.length,
        error: scanStatus?.error,
        warnings: scanStatus?.warnings
      });
      this.host.scanError = null;
    } catch (error) {
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision) || !this.host.gates.canCommitStatus(statusOperation)) return;
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision)) return;
      if (updated) this.host.applyStationList(updated, revision);
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!this.host.gates.canCommitOperation(operationEpoch) || !this.host.listRevisions.isCurrent(revision) || !this.host.gates.canCommitStatus(statusOperation)) return;
      if (this.host.stoppingScan || scanStatus?.state === 'cancelled') {
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
      if (!this.host.disposed && this.host.globalOperation === 'scanning') this.host.globalOperation = 'idle';
      if (!this.host.disposed && !this.host.stopRequestPending && !this.host.externalScanning) this.host.stoppingScan = false;
      this.host.maybeEndScanTimer();
    }
  }

  async stopScan() {
    if (!this.host.scanningActive || this.host.stoppingScan) return;
    const operationEpoch = this.host.gates.currentScanEpoch;
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
        await this.host.externalScan.finishStop(operationEpoch, () => this.host.stopRequestGeneration === requestGeneration);
      } else {
        if (this.host.globalOperation !== 'scanning') this.host.stoppingScan = false;
        this.host.statusMessage = t('Scan stopped.');
        this.host.maybeEndScanTimer();
      }
    } catch (error) {
      if (!this.host.gates.canCommitOperation(operationEpoch)) return;
      this.host.stopRequestPending = false;
      this.host.stoppingScan = false;
      this.host.statusMessage = withDetail('Unable to stop scan', error);
      pushToast(this.host.statusMessage);
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
