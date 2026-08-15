import type { station } from '../../wailsjs/go/models';
import { formatTerminalScanResult, isTerminalScanState } from './result-format';
import { t } from './i18n.svelte';
import type { StationInfo } from './types';

export interface ExternalScanEvent {
  id: number;
  // The manager's scan-status identity for this scan. Terminal handlers use
  // it to verify that the GetScanStatus record they read back describes this
  // exact scan instead of a newer one that already overwrote it.
  statusId?: number;
  stations?: StationInfo[];
  error?: string;
}

// Everything the coordinator needs from the host component. Kept narrow and
// explicit so the coordination logic stays testable without a UI.
export interface ExternalScanHost {
  isDisposed(): boolean;
  // True while the component's own local scan is running.
  localScanRunning(): boolean;
  // Re-check non-scan owners after the asynchronous backend probe. An
  // automatic sleep or another operation can start while that probe is in
  // flight, and its internal scan must never be exposed as user-stoppable.
  canAdoptUnknownScan(): boolean;
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

// A pending recovery retries on every poll while its status read cannot be
// matched (for example a newer scan overwrote the shared scan-status record
// and that scan's events never arrived). Bound the retries so a permanently
// unmatched recovery cannot starve the periodic full status refresh forever.
const MAX_RECOVERY_ATTEMPTS = 8;

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
  // The scan-status identity a pending recovery must match, when known from
  // the terminal event that claimed it. Null when the recovery was marked
  // without an event (a stop or a poll observing the scan's end); such
  // recoveries accept any terminal status, matching the pre-identity rule.
  private recoveryStatusId: number | null = null;
  // A local stop recovered the displayed scan before its terminal event was
  // delivered (the common order for adopted scans, whose id is unknown). The
  // next untracked terminal event belongs to that already-recovered scan and
  // must be consumed instead of being remembered for a redundant recovery.
  private stoppedScanTerminalPending = false;
  // Consecutive polls where a pending recovery made no progress. Capped so an
  // unmatched terminal status cannot rerun the recovery reads forever.
  private recoveryAttempts = 0;

  constructor(private readonly host: ExternalScanHost) {}

  resetForLocalScan() {
    this.scanID = null;
    this.recoveryEpoch = null;
    this.recoveryStatusEpoch = null;
    this.recoveryStatusId = null;
    this.recoveryAttempts = 0;
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
  // status reads succeed. The status identity is unknown here (no terminal
  // event was observed), so the recovery accepts any terminal status.
  markRecoveryPending() {
    this.recoveryEpoch = this.host.scanEpoch();
    this.recoveryStatusEpoch = this.host.statusEpoch();
    this.recoveryStatusId = null;
    this.recoveryAttempts = 0;
  }

  // Records a fresh recovery claim and resets the bounded-retry counter.
  private claimRecovery(operationEpoch: number, statusEpoch: number, statusId: number | null) {
    this.recoveryEpoch = operationEpoch;
    this.recoveryStatusEpoch = statusEpoch;
    this.recoveryStatusId = statusId;
    this.recoveryAttempts = 0;
  }

  // Counts a poll on which a pending recovery made no progress and, once the
  // cap is reached, drops it. The authoritative station list is re-applied on
  // every periodic poll regardless, so only the terminal status message is
  // given up; keeping the recovery forever would rerun the same unmatched
  // reads on every tick and permanently suppress the full status refresh.
  private noteRecoveryStalled() {
    if (this.recoveryEpoch === null) return;
    this.recoveryAttempts += 1;
    if (this.recoveryAttempts < MAX_RECOVERY_ATTEMPTS) return;
    this.recoveryEpoch = null;
    this.recoveryStatusEpoch = null;
    this.recoveryStatusId = null;
    this.recoveryAttempts = 0;
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
    this.recoveryStatusId = null;
    this.recoveryAttempts = 0;
    this.pendingTerminal = null;
    // A new scan supersedes any stop that is still owed its terminal event:
    // backend ordering guarantees the old terminal is delivered before this
    // new scan's own started event, so the debt cannot belong to this scan.
    this.stoppedScanTerminalPending = false;
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
    this.claimRecovery(operationEpoch, statusOperation, event.statusId ?? null);
    this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    // A non-terminal status belongs to a scan that started after this one
    // finished; discarding it leaves the recovery epochs pending so the
    // periodic check retries with the correct outcome instead of rendering
    // the new scan's starting state as this scan's completion. The same
    // applies to a terminal status whose identity names a different scan.
    const completedScanStatus = await this.host.getScanStatus().catch(() => null);
    const scanStatus = this.terminalStatusFor(completedScanStatus, event.statusId ?? null);
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) return;
    if (!this.host.applyStationList(event.stations || [], revision, capturedStationRevisions)) return;
    if (!this.host.canCommitStatus(statusOperation)) return;
    // Clear the recovery epochs only once the terminal status is committed.
    // Clearing earlier would strand the outcome when a newer status owner
    // rejects this commit, leaving the periodic check nothing to retry.
    if (scanStatus) {
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
    }
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
    this.claimRecovery(operationEpoch, statusOperation, event.statusId ?? null);
    if (!this.host.localScanRunning()) this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return;
    if (updated) {
      this.host.applyStationList(updated, revision, capturedStationRevisions);
    }
    const failedScanStatus = await this.host.getScanStatus().catch(() => null);
    const scanStatus = this.terminalStatusFor(failedScanStatus, event.statusId ?? null);
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) return;
    if (!this.host.canCommitStatus(statusOperation)) return;
    // Clear the recovery epochs only after the failed terminal status is
    // committed; a rejected commit must leave them pending so the periodic
    // check retries instead of silently dropping the failure.
    if (updated && scanStatus) {
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
    }
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
    this.claimRecovery(operationEpoch, statusOperation, event.statusId ?? null);
    if (!this.host.localScanRunning()) this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return;
    if (updated) {
      this.host.applyStationList(updated, revision, capturedStationRevisions);
    }
    if (!this.host.canCommitStatus(statusOperation)) return;
    // The recovery epochs intentionally stay pending: the periodic check's
    // recoverTrackedTerminal rewrites this plain message as the richer
    // terminal result once the backend scan status read succeeds, and drops
    // the recovery when a newer owner supersedes it.
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
    const terminal = this.pendingTerminal;
    this.pendingTerminal = null;
    this.claimRecovery(operationEpoch, statusOperation, terminal?.statusId ?? null);
    this.host.setStoppingScan(false);
    this.host.maybeEndScanTimer();
    const capturedStationRevisions = this.host.snapshotStationRevisions();
    const updated = await this.host.getCurrentStationInfo().catch(() => null);
    if (!this.host.canCommitOperation(operationEpoch) || !this.host.isListRevisionCurrent(revision)) return;
    if (updated) {
      this.host.applyStationList(updated, revision, capturedStationRevisions);
    }
    const readScanStatus = await this.host.getScanStatus().catch(() => null);
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) return;
    if (!this.host.canCommitStatus(statusOperation)) {
      // A newer status owner rejected the commit; leave the recovery epochs
      // pending so the periodic check retries instead of dropping the outcome.
      return;
    }
    const scanStatus = this.terminalStatusFor(readScanStatus, terminal?.statusId ?? null);
    if (!scanStatus) {
      // A non-terminal status, or a terminal status whose identity names a
      // scan other than the untracked one, belongs to a later scan. Leave
      // the recovery epochs pending so the next poll retries instead of
      // rendering the wrong scan's state.
      return;
    }
    // Clear the recovery epochs only once the terminal status is committed,
    // matching the other terminal paths.
    if (updated) {
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
    }
    const found = scanStatus?.found ?? this.host.seenInLatestScanCount();
    this.host.setStatusMessage(formatTerminalScanResult({
      state: scanStatus?.state ?? 'completed',
      found,
      known: this.host.knownStationCount(),
      error: scanStatus?.error,
      warnings: scanStatus?.warnings,
      external: true
    }));
    if (scanStatus?.state === 'failed') {
      this.host.notifyExternalScanFailure(t('External scan failed: {detail}', {
        detail: scanStatus?.error || t('unknown error')
      }));
    }
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
    // IsScanning results are only a point-in-time observation. Revalidate at
    // the adoption boundary so a scan that ended after the caller's poll does
    // not leave the UI in a phantom external-scanning state until the next
    // interval. Capture the epoch as well: a local or event-tracked scan that
    // starts during this read owns the state instead.
    const operationEpoch = this.host.scanEpoch();
    const scanning = await this.host.isScanning().catch(() => false);
    if (this.host.isDisposed() || this.host.scanEpoch() !== operationEpoch ||
      !this.host.canAdoptUnknownScan()) return;
    // A terminal event can land while the confirmation read is pending. Its
    // authoritative outcome wins over the older positive scan observation.
    if (this.pendingTerminal) {
      await this.recoverUntracked();
      return;
    }
    if (!scanning) return;
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
      this.noteRecoveryStalled();
      return;
    }
    if (!this.host.applyStationList(updated, revision, capturedStationRevisions)) {
      this.noteRecoveryStalled();
      return;
    }
    const scanStatus = this.terminalStatusFor(
      await this.host.getScanStatus().catch(() => null),
      this.recoveryStatusId
    );
    if (this.host.isDisposed() || !this.host.isListRevisionCurrent(revision)) {
      return;
    }
    // A non-terminal status, or a terminal status whose identity names a
    // different scan, belongs to a newer scan; keep the recovery epochs
    // pending for a retry instead of rendering its state. If the matching
    // record never appears (its scan's events were lost), the bounded retry
    // count eventually drops the recovery so the periodic check returns to
    // full status refreshes.
    if (!scanStatus) {
      this.noteRecoveryStalled();
      return;
    }
    if (this.recoveryStatusEpoch === null || this.recoveryStatusEpoch < statusOperation) {
      // The status line moved to a strictly newer owner after this recovery
      // was claimed. Status epochs only advance, so the terminal message can
      // never commit again; drop the recovery instead of re-running it every
      // poll. The authoritative list was already applied above.
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
      return;
    }
    if (this.recoveryStatusEpoch !== statusOperation || !this.host.canCommitStatus(statusOperation)) {
      // The recovery epoch is ahead of this poll's captured epoch (a terminal
      // event claimed the line after the poll started) or a newer owner took
      // the line while this tick's reads were in flight. Keep the recovery
      // epochs pending so a later tick commits or supersedes them, matching
      // the terminal event handlers.
      return;
    }
    // Clear the recovery epochs only once the terminal status is committed.
    this.recoveryEpoch = null;
    this.recoveryStatusEpoch = null;
    const found = scanStatus?.found ?? this.host.seenInLatestScanCount();
    this.host.setStatusMessage(formatTerminalScanResult({
      state: scanStatus?.state ?? 'completed',
      found, known: this.host.knownStationCount(),
      error: scanStatus?.error,
      warnings: scanStatus?.warnings,
      external: true
    }));
    if (scanStatus?.state === 'failed') {
      this.host.notifyExternalScanFailure(t('External scan failed: {detail}', {
        detail: scanStatus?.error || t('unknown error')
      }));
    }
  }

  // Completes a stop whose StopScan promise settled while this coordinator
  // owned the displayed scan. Rechecks the backend because the scan may have
  // ended on its own while the stop request was in flight. The terminal
  // status write is gated by the stop's own status-operation epoch: an
  // auto-sleep or HTTP event that advanced the status line while StopScan was
  // pending owns the message, and re-deriving ownership from the current
  // epoch would let this late write clobber it.
  async finishStop(
    operationEpoch: number,
    statusOperation: number,
    isStopRequestCurrent: () => boolean
  ): Promise<StopOutcome> {
    const stillScanning = await this.host.isScanning().catch(() => true);
    if (!this.host.canCommitOperation(operationEpoch) || !isStopRequestCurrent()) return 'aborted';
    if (stillScanning) {
      this.host.setStatusMessage(t('Stopping scan...'));
      return 'still-scanning';
    }
    // The backend always delivers a terminal event for a stopped scan. Since
    // this stop now owns the outcome, consume that event when it arrives
    // untracked instead of letting it queue a redundant recovery.
    this.stoppedScanTerminalPending = true;
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
    // A non-terminal status belongs to a scan that started after the stop;
    // aborting keeps the recovery epochs pending for the periodic check.
    if (!scanStatus || !isTerminalScanState(scanStatus.state)) return 'aborted';
    if (this.host.canCommitStatus(statusOperation)) {
      // Clear the recovery epochs only once the terminal status is committed;
      // a rejected commit must leave them pending so the periodic check can
      // retry instead of silently dropping the stop outcome.
      this.recoveryEpoch = null;
      this.recoveryStatusEpoch = null;
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

  // Returns the terminal status record only when it describes the recovered
  // scan. GetScanStatus is a single shared record; a scan that started and
  // ended while this read was in flight overwrites it, and accepting that
  // record would attribute the wrong found count, warnings, and outcome. A
  // missing identity on either side falls back to accepting any terminal
  // status, matching the pre-identity behavior for stops and poll-marked
  // recoveries that never observed the scan's terminal event.
  private terminalStatusFor(
    scanStatus: station.ScanStatus | null,
    expectedStatusId: number | null
  ): station.ScanStatus | null {
    if (!scanStatus || !isTerminalScanState(scanStatus.state)) return null;
    if (expectedStatusId !== null && scanStatus.id !== undefined && scanStatus.id !== expectedStatusId) return null;
    return scanStatus;
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
    if (this.stoppedScanTerminalPending) {
      // The local stop already recovered this scan's terminal outcome. The
      // backend still delivers its terminal event; consume it instead of
      // remembering it, so the next poll does not replay a full recovery
      // (which would clear operation state and rewrite the status line).
      // Still advance latestID so a delayed start with this id stays dropped.
      this.stoppedScanTerminalPending = false;
      if (event.id > this.latestID) this.latestID = event.id;
      return;
    }
    if (event.id <= this.latestID) return;
    this.pendingTerminal = event;
    this.latestID = event.id;
  }
}
