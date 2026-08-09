import type { station } from '../../wailsjs/go/models';
import { formatTerminalScanResult } from './result-format';
import { t } from './i18n.svelte';
import type { StationInfo } from './types';

export interface ExternalScanEvent {
  id: number;
  stations?: StationInfo[];
  error?: string;
}

// Everything the coordinator needs from the host component. Kept narrow and
// explicit so the coordination logic stays testable without a UI.
export interface ExternalScanHost {
  isDisposed(): boolean;
  // True while the component's own local scan is running.
  localScanRunning(): boolean;
  externalScanning(): boolean;
  setExternalScanning(value: boolean): void;
  scanEpoch(): number;
  statusEpoch(): number;
  beginScanEpoch(): number;
  beginStatusOperation(): number;
  canCommitOperation(epoch: number): boolean;
  canCommitStatus(epoch: number): boolean;
  nextListRevision(): number;
  isListRevisionCurrent(revision: number): boolean;
  snapshotStationRevisions(): Map<string, number>;
  prepareForScan(clearOperations?: boolean): void;
  applyStationList(
    updated: StationInfo[] | null | undefined,
    revision: number,
    capturedStationRevisions?: Map<string, number>
  ): boolean;
  seenInLatestScanCount(): number;
  knownStationCount(): number;
  setStatusMessage(message: string): void;
  setStoppingScan(value: boolean): void;
  beginScanTimer(): void;
  maybeEndScanTimer(): void;
  isScanning(): Promise<boolean>;
  getScanStatus(): Promise<station.ScanStatus>;
  getCurrentStationInfo(): Promise<StationInfo[]>;
  notifyExternalScanFailure(message: string): void;
}

export type StopOutcome = 'still-scanning' | 'recovered' | 'aborted';

// State machine for scans started outside this UI (the HTTP API). Tracks
// scan identity, claims terminal events, remembers untracked terminals, and
// drives the recovery reads that reconcile the list and status line after an
// external scan ends.
export class ExternalScanCoordinator {
  private scanID: number | null = null;
  private latestID = 0;
  private pendingTerminal: ExternalScanEvent | null = null;
  private recoveryEpoch: number | null = null;
  private recoveryStatusEpoch: number | null = null;

  constructor(private readonly host: ExternalScanHost) {}

  resetForLocalScan() {
    this.scanID = null;
    this.recoveryEpoch = null;
    this.recoveryStatusEpoch = null;
    this.pendingTerminal = null;
  }

  hasPendingTerminal(): boolean {
    return this.pendingTerminal !== null;
  }

  hasPendingRecovery(): boolean {
    return this.recoveryEpoch === this.host.scanEpoch();
  }

  // The poll observed the end of an external scan it was displaying; remember
  // the current epochs so the terminal outcome is recovered once the list and
  // status reads succeed.
  markRecoveryPending() {
    this.recoveryEpoch = this.host.scanEpoch();
    this.recoveryStatusEpoch = this.host.statusEpoch();
  }

  handleStarted(event: ExternalScanEvent) {
    if (this.host.isDisposed()) return;
    if (event.id <= this.latestID) return;
    this.latestID = event.id;
    this.begin(event.id);
  }

  private begin(id: number | null) {
    this.host.beginScanEpoch();
    this.host.nextListRevision();
    this.host.prepareForScan(false);
    this.recoveryEpoch = null;
    this.recoveryStatusEpoch = null;
    this.pendingTerminal = null;
    this.scanID = id;
    this.host.setExternalScanning(true);
    this.host.setStoppingScan(false);
    this.host.beginScanTimer();
    this.host.setStatusMessage(t('Preparing external scan...'));
  }

  async handleCompleted(event: ExternalScanEvent): Promise<void> {
    const claimed = this.scanID === null
      ? await this.claimUnknownTerminal(event)
      : this.claimTrackedTerminal(event);
    if (!claimed) {
      this.rememberUntrackedTerminal(event);
      return;
    }
    const statusOperation = this.host.beginStatusOperation();
    const operationEpoch = this.host.beginScanEpoch();
    const revision = this.host.nextListRevision();
    this.host.prepareForScan();
    this.recoveryEpoch = operationEpoch;
    this.recoveryStatusEpoch = statusOperation;
    this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const scanStatus = await this.host.getScanStatus().catch(() => null);
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) return;
    if (scanStatus) {
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
    }
    if (!this.host.applyStationList(event.stations || [], revision, capturedStationRevisions)) return;
    if (!this.host.canCommitStatus(statusOperation)) return;
    const found = scanStatus?.found ?? this.host.seenInLatestScanCount();
    this.host.setStatusMessage(formatTerminalScanResult({
      state: 'completed', found, known: this.host.knownStationCount(),
      warnings: scanStatus?.warnings, external: true
    }));
  }

  async handleFailed(event: ExternalScanEvent): Promise<void> {
    const claimed = this.scanID === null
      ? await this.claimUnknownTerminal(event)
      : this.claimTrackedTerminal(event);
    if (!claimed) {
      this.rememberUntrackedTerminal(event);
      return;
    }
    const statusOperation = this.host.beginStatusOperation();
    const message = event.error || t('unknown error');
    const operationEpoch = this.host.beginScanEpoch();
    const revision = this.host.nextListRevision();
    this.host.prepareForScan();
    this.recoveryEpoch = operationEpoch;
    this.recoveryStatusEpoch = statusOperation;
    if (!this.host.localScanRunning()) this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return;
    if (updated) {
      this.host.applyStationList(updated, revision, capturedStationRevisions);
    }
    const scanStatus = await this.host.getScanStatus().catch(() => null);
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) return;
    if (updated && scanStatus) {
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
    }
    if (!this.host.canCommitStatus(statusOperation)) return;
    this.host.setStatusMessage(formatTerminalScanResult({
      state: 'failed', error: message, known: this.host.knownStationCount(),
      warnings: scanStatus?.warnings, external: true
    }));
    this.host.notifyExternalScanFailure(t('External scan failed: {detail}', { detail: message }));
  }

  async handleCancelled(event: ExternalScanEvent): Promise<void> {
    const claimed = this.scanID === null
      ? await this.claimUnknownTerminal(event)
      : this.claimTrackedTerminal(event);
    if (!claimed) {
      this.rememberUntrackedTerminal(event);
      return;
    }
    const statusOperation = this.host.beginStatusOperation();
    const operationEpoch = this.host.beginScanEpoch();
    const revision = this.host.nextListRevision();
    this.host.prepareForScan();
    this.recoveryEpoch = operationEpoch;
    this.recoveryStatusEpoch = statusOperation;
    if (!this.host.localScanRunning()) this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return;
    if (updated) {
      this.host.applyStationList(updated, revision, capturedStationRevisions);
    }
    if (!this.host.canCommitStatus(statusOperation)) return;
    this.host.setStatusMessage(t('Scan stopped.'));
  }

  // Applies the terminal outcome of a scan that ended while untracked. The
  // commit is gated like the terminal event handlers, and an incomplete
  // status read leaves the recovery epochs pending so the periodic check
  // retries.
  async recoverUntracked(): Promise<void> {
    const statusOperation = this.host.beginStatusOperation();
    const operationEpoch = this.host.beginScanEpoch();
    const revision = this.host.nextListRevision();
    this.host.prepareForScan();
    this.pendingTerminal = null;
    this.recoveryEpoch = operationEpoch;
    this.recoveryStatusEpoch = statusOperation;
    this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return;
    if (updated) {
      this.host.applyStationList(updated, revision, capturedStationRevisions);
    }
    const scanStatus = await this.host.getScanStatus().catch(() => null);
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) return;
    if (updated && scanStatus) {
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
    }
    if (!this.host.canCommitStatus(statusOperation)) return;
    const found = scanStatus?.found ?? this.host.seenInLatestScanCount();
    this.host.setStatusMessage(formatTerminalScanResult({
      state: scanStatus?.state ?? 'completed',
      found,
      known: this.host.knownStationCount(),
      error: scanStatus?.error,
      warnings: scanStatus?.warnings,
      external: true
    }));
  }

  async adoptUnknown(): Promise<void> {
    // The scan observed by IsScanning may have terminated before this
    // continuation ran; its terminal event then arrived while no listener
    // tracked the scan. Recover that finished outcome instead of entering a
    // stale scanning state.
    if (this.pendingTerminal) {
      await this.recoverUntracked();
      return;
    }
    this.begin(null);
  }

  // Terminal recovery for a scan the poll displayed: applies the authoritative
  // list and scan status, retrying on a later tick when either read fails.
  async recoverTrackedTerminal(
    revision: number,
    statusOperation: number,
    capturedStationRevisions: Map<string, number>
  ): Promise<void> {
    // Match recoverUntracked: a transient list-read rejection must leave the
    // recovery epochs pending for a retry instead of escaping to the periodic
    // check, which would overwrite the status with a misleading error.
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (updated === null) {
      return;
    }
    if (!this.host.applyStationList(updated, revision, capturedStationRevisions)) {
      return;
    }
    const scanStatus = await this.host.getScanStatus().catch(() => null);
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) {
      return;
    }
    if (!scanStatus) {
      return;
    }
    this.recoveryEpoch = null;
    const canWriteTerminalStatus = this.recoveryStatusEpoch === statusOperation &&
      this.host.canCommitStatus(statusOperation);
    this.recoveryStatusEpoch = null;
    if (!canWriteTerminalStatus) return;
    const found = scanStatus?.found ?? this.host.seenInLatestScanCount();
    this.host.setStatusMessage(formatTerminalScanResult({
      state: scanStatus?.state ?? 'completed',
      found, known: this.host.knownStationCount(),
      error: scanStatus?.error,
      warnings: scanStatus?.warnings,
      external: true
    }));
  }

  // Completes a stop whose StopScan promise settled while this coordinator
  // owned the displayed scan. Rechecks the backend because the scan may have
  // ended on its own while the stop request was in flight.
  async finishStop(operationEpoch: number, isStopRequestCurrent: () => boolean): Promise<StopOutcome> {
    const stillScanning = await this.host.isScanning().catch(() => true);
    if (!this.host.canCommitOperation(operationEpoch) || !isStopRequestCurrent()) return 'aborted';
    if (stillScanning) {
      this.host.setStatusMessage(t('Stopping scan...'));
      return 'still-scanning';
    }
    this.host.setExternalScanning(false);
    this.scanID = null;
    this.host.setStoppingScan(false);
    this.markRecoveryPending();
    const revision = this.host.nextListRevision();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return 'aborted';
    if (!updated || !this.host.applyStationList(updated, revision, capturedStationRevisions)) return 'aborted';
    const scanStatus = await this.host.getScanStatus().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return 'aborted';
    if (!scanStatus) return 'aborted';
    this.recoveryEpoch = null;
    const canWriteTerminalStatus = this.recoveryStatusEpoch === this.host.statusEpoch();
    this.recoveryStatusEpoch = null;
    if (canWriteTerminalStatus) {
      const found = scanStatus?.found ?? this.host.seenInLatestScanCount();
      this.host.setStatusMessage(formatTerminalScanResult({
        state: scanStatus?.state ?? 'cancelled', found, known: this.host.knownStationCount(),
        error: scanStatus?.error, warnings: scanStatus?.warnings, external: true
      }));
    }
    this.host.maybeEndScanTimer();
    return 'recovered';
  }

  private claimTrackedTerminal(event: ExternalScanEvent): boolean {
    if (!this.host.externalScanning() || (this.scanID !== null && event.id !== this.scanID)) return false;
    this.latestID = Math.max(this.latestID, event.id);
    this.host.setExternalScanning(false);
    this.scanID = null;
    return true;
  }

  private async claimUnknownTerminal(event: ExternalScanEvent): Promise<boolean> {
    if (event.id <= this.latestID || !this.host.externalScanning() || this.scanID !== null ||
      !(await this.matchesUnknownExternalScan())) return false;
    // An adopted scan has no ID until its terminal event. Claim it before any
    // further awaits so concurrent terminal notifications cannot both commit.
    return this.claimTrackedTerminal(event);
  }

  private async matchesUnknownExternalScan(): Promise<boolean> {
    // The app may have attached after the start event. In that case adopt the
    // first terminal ID only after confirming the backend scan has ended.
    if (!this.host.externalScanning() || this.scanID !== null) return false;
    const epoch = this.host.scanEpoch();
    const scanning = await this.host.isScanning().catch(() => true);
    return this.host.externalScanning() && this.scanID === null && this.host.scanEpoch() === epoch && !scanning;
  }

  // A terminal event for a scan the UI never tracked must not be dropped: if
  // a later IsScanning() result still reports that scan as running, adopting
  // it would create a stale "scanning" state. Remember the newest untracked
  // terminal so adoption can recover the finished outcome instead, and so
  // its id can never be adopted through a delayed start event.
  private rememberUntrackedTerminal(event: ExternalScanEvent) {
    if (this.host.externalScanning() || this.scanID !== null) return;
    if (event.id <= this.latestID) return;
    this.pendingTerminal = event;
    this.latestID = event.id;
  }
}
