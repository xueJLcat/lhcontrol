import { EventsOn } from '../../../wailsjs/runtime/runtime';
import { station as stationModels } from '../../../wailsjs/go/models';
import {
  CheckAllStationStatuses,
  GetCurrentStationInfo,
  GetScanStatus,
  IdentifyStation,
  IsScanning,
  RefreshStationCapabilities,
  RenameStationByAddress,
  ScanAndFetchStations,
  SetAllStationsPowerDetailed,
  SetStationChannel,
  SetStationPower,
  StopScan
} from '../backend';
import type { PowerFeedback, PowerTarget, StationInfo } from '../types';
import { scanErrorCopy, classifyScanError, type ScanErrorInfo } from '../scan-error';
import {
  canSetPower, channelChangeBlockedReason, hasCurrentChannel, hasStableConfirmedPowerState,
  powerTargetLabel, sameStationInfo, stateLabel
} from '../station';
import { formatBulkResult, formatTerminalScanResult, summarizeBulkResult } from '../result-format';
import { pushToast } from '../toast';
import { deriveOperationLocks, type GlobalOperation } from '../operation-state';
import { ExternalScanCoordinator, type ExternalScanEvent } from '../external-scan';
import { OperationGate } from '../operation-gate';
import { PowerFeedbackRegistry } from '../power-feedback';
import { RevisionGate } from '../revision-gate';
import { ScanTimer } from '../scan-timer';
import { ApiStatusPoller } from '../api-status-poller';
import { ChannelMemory } from '../channel-memory';
import { t, withDetail } from '../i18n.svelte';

export interface AutoSleepEvent {
  phase: 'started' | 'completed' | 'cancelled' | 'skipped' | 'failed';
  success?: number;
  failed?: number;
  skipped?: number;
  error?: string;
  updateId?: number;
  stations?: StationInfo[];
}

export interface ExternalStationUpdateEvent {
  id: number;
  source: string;
  stations: StationInfo[];
}

// Overlay controls owned by App. The store calls back into them whenever an
// operation must close or open a layer instead of reaching into UI state.
export interface StationStoreUi {
  closeChannelEditor(): void;
  clearBulkConfirmation(): void;
  requestBulkConfirmation(target: PowerTarget): void;
}

// All fleet state and backend orchestration. Held in one rune-backed class so
// App.svelte stays a pure composition + overlay-switching shell. Effects are
// deliberately absent here ($effect cannot run outside components); App wires
// the two render-cycle hooks via syncChannelMemory() and explicit calls.
export class StationStore {
  stations = $state<StationInfo[]>([]);
  statusMessage = $state(t('Ready to scan.'));
  gattOperations = $state(new Set<string>());
  configOperations = $state(new Set<string>());
  powerTargetByAddress = $state<Record<string, PowerTarget | undefined>>({});
  powerFeedbackMap = $state<Record<string, PowerFeedback | undefined>>({});
  globalOperation = $state<GlobalOperation>('idle');
  bulkTarget = $state<PowerTarget | null>(null);
  editingAddress = $state<string | null>(null);
  channelError = $state('');
  channelWarning = $state(false);
  scanError = $state<ScanErrorInfo | null>(null);
  externalScanning = $state(false);
  autoSleepRunning = $state(false);
  stoppingScan = $state(false);
  scanElapsed = $state(0);
  apiRunning = $state(false);
  apiAddress = $state('');
  apiError = $state('');
  configWarnings = $state<string[]>([]);
  configWritable = $state(true);

  private statusCheckInterval: ReturnType<typeof setInterval> | null = null;
  private cancelExternalScanListener: (() => void) | null = null;
  private cancelExternalScanFailureListener: (() => void) | null = null;
  private cancelExternalScanStartedListener: (() => void) | null = null;
  private cancelExternalScanCancelledListener: (() => void) | null = null;
  private cancelAutoSleepListener: (() => void) | null = null;
  private cancelExternalStationUpdateListener: (() => void) | null = null;
  private lastExternalUpdateId = 0;
  private stopRequestPending = false;
  private stopRequestGeneration = 0;
  private disposed = false;
  private startupPending = true;

  private powerFeedback = new PowerFeedbackRegistry((next) => {
    this.powerFeedbackMap = next;
  });
  private scanTimer = new ScanTimer((seconds) => {
    this.scanElapsed = seconds;
  });
  private listRevisions = new RevisionGate();
  private gates = new OperationGate();

  // Display-only channel memory (see ChannelMemory): keeps the channel bar,
  // card chip and card ordering stable across transient backend wipes.
  private channelMemory = new ChannelMemory();

  // Coordination for scans started outside this UI (the HTTP API). All host
  // callbacks write through store state so Svelte reactivity keeps the
  // template in sync.
  private externalScan = new ExternalScanCoordinator({
    isDisposed: () => this.disposed,
    localScanRunning: () => this.globalOperation === 'scanning',
    externalScanning: () => this.externalScanning,
    setExternalScanning: (value) => this.externalScanning = value,
    scanEpoch: () => this.gates.currentScanEpoch,
    statusEpoch: () => this.gates.currentStatusEpoch,
    beginScanEpoch: () => this.gates.beginScanEpoch(),
    beginStatusOperation: () => this.gates.beginStatusOperation(),
    canCommitOperation: (epoch) => this.gates.canCommitOperation(epoch),
    canCommitStatus: (epoch) => this.gates.canCommitStatus(epoch),
    nextListRevision: () => this.listRevisions.next(),
    isListRevisionCurrent: (revision) => this.listRevisions.isCurrent(revision),
    snapshotStationRevisions: () => this.gates.snapshotStationRevisions(),
    prepareForScan: (clearOperations) => this.prepareForScan(clearOperations),
    applyStationList: (updated, revision, captured) => this.applyStationList(updated, revision, captured),
    seenInLatestScanCount: () => this.stations.filter((station) => station.seenInLatestScan).length,
    knownStationCount: () => this.stations.length,
    setStatusMessage: (message) => this.statusMessage = message,
    setStoppingScan: (value) => this.stoppingScan = value,
    beginScanTimer: () => this.beginScanTimer(),
    maybeEndScanTimer: () => this.maybeEndScanTimer(),
    isScanning: () => IsScanning(),
    getScanStatus: () => GetScanStatus(),
    getCurrentStationInfo: () => GetCurrentStationInfo(),
    notifyExternalScanFailure: (message) => pushToast(message)
  });

  // HTTP API health polling and config-warning de-duplication live in a
  // dedicated poller; the callbacks write through store state so Svelte
  // reactivity keeps the template in sync.
  private apiStatus = new ApiStatusPoller({
    isDisposed: () => this.disposed,
    commitStatus: (status) => {
      this.apiRunning = status.running;
      this.apiError = status.error;
      this.apiAddress = status.address;
      this.configWarnings = status.warnings ?? [];
      this.configWritable = status.configWritable ?? true;
    },
    commitFailure: (error) => {
      this.apiRunning = false;
      this.apiError = error;
    },
    reportConfigWarning: (warning) => pushToast(warning, 'warning')
  });

  constructor(private ui: StationStoreUi) {}

  displayChannel(station: StationInfo): number {
    return this.channelMemory.displayChannel(station);
  }

  // Called from App's $effect.pre before any derived list recomputes so the
  // memory cache is fresh when sort keys and child props are evaluated.
  syncChannelMemory() {
    this.channelMemory.refresh(this.stations);
  }

  sortedStations = $derived([...this.stations].sort((a, b) => {
    const ac = this.displayChannel(a) || Number.MAX_SAFE_INTEGER;
    const bc = this.displayChannel(b) || Number.MAX_SAFE_INTEGER;
    return ac - bc || a.name.localeCompare(b.name) || a.address.localeCompare(b.address);
  }));

  private conflictStations = $derived(this.stations.filter((station) => station.channelConflict));
  conflictDetails = $derived((() => {
    const byChannel = new Map<number, string[]>();
    for (const station of this.conflictStations) {
      const key = station.channel > 0 ? station.channel : -1;
      byChannel.set(key, [...(byChannel.get(key) ?? []), station.name]);
    }
    return [...byChannel.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([channel, names]) => channel > 0 ? `CH ${channel}: ${names.join(' + ')}` : names.join(' + '))
      .join(' · ');
  })());

  fleetOn = $derived(this.stations.filter((station) => hasStableConfirmedPowerState(station, 'on')).length);
  fleetStandby = $derived(this.stations.filter((station) => hasStableConfirmedPowerState(station, 'standby')).length);
  fleetSleep = $derived(this.stations.filter((station) => hasStableConfirmedPowerState(station, 'sleep')).length);
  // Stations that exist but have no verified state (stale, unconfirmed or
  // booting) still count, so the summary never goes quiet while cards exist.
  fleetUnverified = $derived(Math.max(0, this.stations.length - this.fleetOn - this.fleetStandby - this.fleetSleep));
  actionableOn = $derived(this.stations.filter((station) => canSetPower(station, 'on')));
  actionableStandby = $derived(this.stations.filter((station) => canSetPower(station, 'standby')));
  actionableSleep = $derived(this.stations.filter((station) => canSetPower(station, 'sleep')));

  // Derived instead of a template function call: call expressions hide
  // their reactive inputs from invalidation, and the header's "all at
  // state" flags must follow the station list. The stable-raw check keeps
  // the bulk thumb dark while stations are still booting after a bulk
  // power-on, instead of trusting the backend's heuristic boot fallback.
  allOn = $derived(this.stations.length > 0 && this.stations.every((item) => hasStableConfirmedPowerState(item, 'on')));
  allStandby = $derived(this.stations.length > 0 && this.stations.every((item) => hasStableConfirmedPowerState(item, 'standby')));
  allSleep = $derived(this.stations.length > 0 && this.stations.every((item) => hasStableConfirmedPowerState(item, 'sleep')));

  private operationLocks = $derived(deriveOperationLocks({
    global: this.globalOperation,
    externalScanning: this.externalScanning,
    autoSleepRunning: this.autoSleepRunning,
    gattAddresses: this.gattOperations,
    configAddresses: this.configOperations
  }));
  isLoading = $derived(this.globalOperation === 'scanning');
  isBulkLoading = $derived(this.globalOperation === 'bulk-power');
  isStatusChecking = $derived(this.globalOperation === 'status-refresh');
  scanningActive = $derived(this.isLoading || this.externalScanning || this.stoppingScan);
  // A scan is genuinely in flight. Unlike scanningActive this ignores a stop
  // request that is still settling after its scan already ended, so the header
  // returns to an actionable Scan instead of staying stuck on "Stopping...".
  scanRunning = $derived(this.isLoading || this.externalScanning);
  private anyDeviceOperation = $derived(this.operationLocks.anyDeviceOperation);
  scanLocked = $derived(this.operationLocks.scanLocked);
  bulkLocked = $derived(this.operationLocks.bulkLocked);
  stationLocked = $derived(this.operationLocks.stationLocked);
  // The periodic status reader occupies one of the backend's two device
  // operation slots. Keep the remaining slot available to one foreground
  // action instead of allowing a second action that will fail with Busy.
  private foregroundGattCapacity = $derived(this.isStatusChecking ? 1 : 2);
  private gattCapacityReached = $derived(this.gattOperations.size >= this.foregroundGattCapacity);

  // Fleet trust partition behind the bulk scope row and the confirmation
  // gate: a station counts as verified only while its presence, scan
  // freshness and confirmed power state are all current. Everything else is
  // included in bulk commands at the backend's discretion, which the user
  // must see before confirming.
  visibleCount = $derived(this.stations.filter((station) => station.isPresent && !station.presenceUncertain).length);
  invisibleCount = $derived(this.stations.filter((station) => !station.isPresent).length);
  uncertainCount = $derived(this.stations.filter((station) => station.isPresent && station.presenceUncertain).length);
  untrustedCount = $derived(this.stations.filter((station) =>
    !station.isPresent || station.presenceUncertain || !station.scanFresh ||
    !station.powerStateConfirmed || station.powerState < 0 || station.powerState === 3
  ).length);

  // Template-side lock lookup. Template expressions only invalidate on the
  // variables they reference syntactically, so the reactive inputs are passed
  // explicitly instead of being read inside a helper call.
  private computeGattLocked(
    current: StationInfo[],
    locked: boolean,
    capacityReached: boolean,
    busy: Set<string>
  ): Map<string, boolean> {
    const byAddress = new Map<string, boolean>();
    for (const station of current) {
      byAddress.set(station.address, locked || (capacityReached && !busy.has(station.address)));
    }
    return byAddress;
  }
  gattLockedByAddress = $derived(this.computeGattLocked(this.stations, this.stationLocked, this.gattCapacityReached, this.gattOperations));

  // Same explicit-dependency pattern: the template reads per-address busy
  // flags from this derived map instead of calling stationBusy(), whose
  // reactive inputs a call expression would hide from invalidation.
  private computeBusy(
    current: StationInfo[],
    gatt: Set<string>,
    config: Set<string>
  ): Map<string, boolean> {
    const byAddress = new Map<string, boolean>();
    for (const station of current) {
      byAddress.set(station.address, gatt.has(station.address) || config.has(station.address));
    }
    return byAddress;
  }
  busyByAddress = $derived(this.computeBusy(this.stations, this.gattOperations, this.configOperations));

  // Channel-map occupancy as seen by the editor: the station being edited
  // keeps its own channel available. Parameterized because the exclusion
  // address is overlay state owned by App.
  occupiedChannelsExcluding(selectedAddress: string | null): Map<number, string[]> {
    const occupied = new Map<number, string[]>();
    const candidates = this.stations
      .filter((station) => hasCurrentChannel(station) && station.address !== selectedAddress)
      .sort((a, b) => a.name.localeCompare(b.name) || a.address.localeCompare(b.address));
    for (const station of candidates) {
      occupied.set(station.channel, [...(occupied.get(station.channel) ?? []), station.name]);
    }
    return occupied;
  }

  hasUnknownVisibleChannelExcluding(selectedAddress: string | null): boolean {
    return this.stations.some(
      (station) => station.isPresent && station.address !== selectedAddress &&
        (station.presenceUncertain || !station.scanFresh || !station.channelFresh || station.channel === 0)
    );
  }

  stationBusy(address: string): boolean {
    return this.gattOperations.has(address) || this.configOperations.has(address);
  }

  private gattLockedFor(address: string): boolean {
    return this.stationLocked || (this.gattCapacityReached && !this.gattOperations.has(address));
  }

  private beginScanTimer() {
    this.scanTimer.begin();
  }

  private maybeEndScanTimer() {
    // A local scan and an external scan can overlap; the timer belongs to
    // whichever is still running, so only stop when both have finished.
    if (this.globalOperation !== 'scanning' && !this.externalScanning) this.scanTimer.end();
  }

  private setGattBusy(address: string, busy: boolean) {
    const next = new Set(this.gattOperations);
    if (busy) next.add(address);
    else next.delete(address);
    this.gattOperations = next;
  }

  private setConfigBusy(address: string, busy: boolean) {
    const next = new Set(this.configOperations);
    if (busy) next.add(address);
    else next.delete(address);
    this.configOperations = next;
  }

  private commitStations(updated: StationInfo[]) {
    // Reuse the previous object for unchanged stations so no-op background
    // refreshes do not re-render cards or retrigger CSS transitions.
    const previousByAddress = new Map(this.stations.map((station) => [station.address, station]));
    this.stations = updated.map((station) => {
      const previous = previousByAddress.get(station.address);
      return previous && sameStationInfo(previous, station) ? previous : station;
    });
    this.powerFeedback.reconcile(this.stations);
  }

  applyStationList(
    updated: StationInfo[] | null | undefined,
    revision: number,
    capturedStationRevisions?: Map<string, number>
  ): boolean {
    if (this.disposed || !this.listRevisions.isCurrent(revision)) return false;
    const incoming = updated || [];
    if (!capturedStationRevisions) {
      this.commitStations(incoming);
      return true;
    }
    const currentByAddress = new Map(this.stations.map((station) => [station.address, station]));
    const incomingAddresses = new Set(incoming.map((station) => station.address));
    const merged = incoming.map((station) =>
      this.gates.stationRevision(station.address) === (capturedStationRevisions.get(station.address) ?? 0)
        ? station
        : currentByAddress.get(station.address) ?? station
    );
    for (const current of this.stations) {
      if (!incomingAddresses.has(current.address) &&
        this.gates.stationRevision(current.address) !== (capturedStationRevisions.get(current.address) ?? 0)) {
        merged.push(current);
      }
    }
    this.commitStations(merged);
    return true;
  }

  private prepareForScan(clearOperations = true) {
    this.cancelRename();
    this.ui.closeChannelEditor();
    this.channelError = '';
    this.channelWarning = false;
    this.scanError = null;
    this.ui.clearBulkConfirmation();
    // A locally running scan has acquired the backend's exclusive operation
    // lock. An accepted external scan may still be waiting for existing work,
    // so preserve those visible operations until their promises settle.
    if (clearOperations) {
      this.gattOperations = new Set();
      this.configOperations = new Set();
      this.gates.clearOperationTokens();
    }
    this.powerTargetByAddress = {};
    this.powerFeedback.clearAll();
  }

  private mergeStationUpdates(updated: StationInfo[]) {
    if (!updated.length) return;
    const byAddress = new Map(updated.map((station) => [station.address, station]));
    const existingAddresses = new Set(this.stations.map((station) => station.address));
    this.commitStations([
      ...this.stations.map((station) => byAddress.get(station.address) ?? station),
      ...updated.filter((station) => !existingAddresses.has(station.address))
    ]);
  }

  private withStationChanges(current: StationInfo, changes: Partial<StationInfo>): StationInfo {
    return stationModels.StationInfo.createFrom({ ...current, ...changes });
  }

  mount() {
    const startupScanEpoch = this.gates.currentScanEpoch;
    this.apiStatus.start(15000);
    this.cancelExternalScanStartedListener = EventsOn('external-scan-started', (value: unknown) => {
      this.externalScan.handleStarted(value as ExternalScanEvent);
    });
    this.cancelExternalScanListener = EventsOn('external-scan-completed', (value: unknown) => {
      this.externalScan.handleCompleted(value as ExternalScanEvent);
    });
    this.cancelExternalScanFailureListener = EventsOn('external-scan-failed', (value: unknown) => {
      this.externalScan.handleFailed(value as ExternalScanEvent);
    });
    this.cancelExternalScanCancelledListener = EventsOn('external-scan-cancelled', (value: unknown) => {
      this.externalScan.handleCancelled(value as ExternalScanEvent);
    });
    this.cancelAutoSleepListener = EventsOn('auto-sleep', (value: unknown) => {
      this.handleAutoSleepEvent(value as AutoSleepEvent);
    });
    this.cancelExternalStationUpdateListener = EventsOn('external-stations-updated', (value: unknown) => {
      this.handleExternalStationUpdate(value as ExternalStationUpdateEvent);
    });
    this.statusCheckInterval = setInterval(() => {
      void this.periodicStatusCheck();
    }, 15000);
    void (async () => {
      const startupScanning = await IsScanning().catch(() => false);
      // Do not allow the first polling tick to acquire the backend operation
      // lock before the initial external-scan check can start the local scan.
      this.startupPending = false;
      // An external scan event may have arrived while this initial query was
      // pending. Do not let its older result overwrite the newer event state.
      if (this.disposed || startupScanEpoch !== this.gates.currentScanEpoch) return;
      if (startupScanning) {
        await this.externalScan.adoptUnknown();
      } else {
        await this.startScan();
      }
    })();
  }

  dispose() {
    this.disposed = true;
    this.gates.dispose();
    this.listRevisions.dispose();
    this.apiStatus.dispose();
    this.scanTimer.dispose();
    if (this.statusCheckInterval) clearInterval(this.statusCheckInterval);
    this.powerFeedback.clearAll();
    this.cancelExternalScanListener?.();
    this.cancelExternalScanFailureListener?.();
    this.cancelExternalScanStartedListener?.();
    this.cancelExternalScanCancelledListener?.();
    this.cancelAutoSleepListener?.();
    this.cancelExternalStationUpdateListener?.();
  }

  private async periodicStatusCheck() {
    if (this.startupPending || this.autoSleepRunning || this.isStatusChecking || this.isLoading || this.isBulkLoading || this.anyDeviceOperation) return;
    this.globalOperation = 'status-refresh';
    const statusOperation = this.gates.currentStatusEpoch;
    const revision = this.listRevisions.next();
    const capturedStationRevisions = this.gates.snapshotStationRevisions();
    try {
      const scanning = await IsScanning();
      if (this.disposed || !this.listRevisions.isCurrent(revision)) return;
      const wasExternalScanning = this.externalScanning;
      if (scanning && !this.isLoading && !this.externalScanning) {
        await this.externalScan.adoptUnknown();
        return;
      }
      this.externalScanning = scanning && !this.isLoading;
      if (!scanning) {
        this.stoppingScan = false;
        if (wasExternalScanning) {
          this.externalScan.markRecoveryPending();
        }
        if (this.externalScan.hasPendingRecovery()) {
          await this.externalScan.recoverTrackedTerminal(revision, statusOperation, capturedStationRevisions);
        } else if (this.externalScan.hasPendingTerminal()) {
          // A scan ended while no listener tracked it. Apply its terminal
          // outcome now instead of waiting for the next external scan event.
          await this.externalScan.recoverUntracked();
          return;
        } else if (!this.applyStationList(await CheckAllStationStatuses(), revision, capturedStationRevisions)) return;
        this.maybeEndScanTimer();
      } else {
        this.beginScanTimer();
        if (this.externalScanning) {
          const scanStatus = await GetScanStatus().catch(() => null);
          // A pending stop owns the status message; letting the poll
          // overwrite "Stopping scan..." contradicts the header button.
          if (!this.disposed && this.listRevisions.isCurrent(revision) && scanStatus &&
            !this.stoppingScan && !this.stopRequestPending) {
            this.statusMessage = scanStatus.state === 'starting'
              ? t('Preparing external scan...')
              : t('External scan in progress...');
          }
        }
      }
    } catch (error) {
      if (this.disposed || !this.listRevisions.isCurrent(revision)) return;
      console.error('Periodic status check failed:', error);
      const fallback = await GetCurrentStationInfo().catch(() => this.stations);
      if (!this.applyStationList(fallback, revision, capturedStationRevisions)) return;
      // While the scan recovery card is up the Bluetooth outage is already
      // explained; repeating this failure in the status line every poll
      // cycle only re-surfaces the same error.
      if (this.gates.canCommitStatus(statusOperation) && !this.scanError) this.statusMessage = withDetail('Status refresh incomplete', error);
    } finally {
      if (!this.disposed && this.globalOperation === 'status-refresh') this.globalOperation = 'idle';
    }
  }

  async startScan() {
    if (this.isLoading || this.scanLocked) return;
    this.prepareForScan();
    // A pending stop belongs to the superseded scan. Its StopScan promise
    // can still be settling while the empty fleet lets the user start a new
    // scan; the new scan must show its own running state and a working Stop.
    // The old stop's finally never clears stoppingScan while a scan runs,
    // so this reset cannot be clobbered.
    this.stoppingScan = false;
    this.externalScan.resetForLocalScan();
    this.globalOperation = 'scanning';
    const statusOperation = this.gates.beginStatusOperation();
    this.beginScanTimer();
    const operationEpoch = this.gates.beginScanEpoch();
    const revision = this.listRevisions.next();
    this.statusMessage = t('Scanning for base stations...');
    try {
      if (!this.applyStationList(await ScanAndFetchStations(), revision)) return;
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!this.gates.canCommitOperation(operationEpoch) || !this.listRevisions.isCurrent(revision) || !this.gates.canCommitStatus(statusOperation)) return;
      const found = scanStatus?.found ?? this.stations.filter((station) => station.seenInLatestScan).length;
      this.statusMessage = formatTerminalScanResult({
        state: scanStatus?.state ?? 'completed',
        found, known: this.stations.length,
        error: scanStatus?.error,
        warnings: scanStatus?.warnings
      });
      this.scanError = null;
    } catch (error) {
      if (!this.gates.canCommitOperation(operationEpoch) || !this.listRevisions.isCurrent(revision) || !this.gates.canCommitStatus(statusOperation)) return;
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!this.gates.canCommitOperation(operationEpoch) || !this.listRevisions.isCurrent(revision)) return;
      if (updated) this.applyStationList(updated, revision);
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!this.gates.canCommitOperation(operationEpoch) || !this.listRevisions.isCurrent(revision) || !this.gates.canCommitStatus(statusOperation)) return;
      if (this.stoppingScan || scanStatus?.state === 'cancelled') {
        if (!this.stopRequestPending) this.stoppingScan = false;
        this.statusMessage = t('Scan stopped.');
        this.scanError = null;
      } else {
        // The persistent recovery card carries the heading, guidance and raw
        // detail; a toast plus a verbatim status line were two extra copies
        // of the same failure. The status line keeps only a short summary.
        const classified = classifyScanError(error);
        this.scanError = classified;
        this.statusMessage = classified.kind === 'unknown'
          ? t('Scan failed.')
          : t('Scan failed: {heading}', { heading: scanErrorCopy(classified).heading });
      }
    } finally {
      if (!this.disposed && this.globalOperation === 'scanning') this.globalOperation = 'idle';
      if (!this.disposed && !this.stopRequestPending && !this.externalScanning) this.stoppingScan = false;
      this.maybeEndScanTimer();
    }
  }

  async stopScan() {
    if (!this.scanningActive || this.stoppingScan) return;
    const operationEpoch = this.gates.currentScanEpoch;
    const requestGeneration = ++this.stopRequestGeneration;
    this.stoppingScan = true;
    this.stopRequestPending = true;
    this.statusMessage = t('Stopping scan...');
    try {
      await StopScan();
      if (!this.gates.canCommitOperation(operationEpoch)) return;
      this.stopRequestPending = false;
      if (!this.stoppingScan) return;
      if (this.externalScanning) {
        await this.externalScan.finishStop(operationEpoch, () => this.stopRequestGeneration === requestGeneration);
      } else {
        if (this.globalOperation !== 'scanning') this.stoppingScan = false;
        this.statusMessage = t('Scan stopped.');
        this.maybeEndScanTimer();
      }
    } catch (error) {
      if (!this.gates.canCommitOperation(operationEpoch)) return;
      this.stopRequestPending = false;
      this.stoppingScan = false;
      this.statusMessage = withDetail('Unable to stop scan', error);
      pushToast(this.statusMessage);
    } finally {
      // Terminal scan events can advance scanEpoch before StopScan settles.
      // Clear this request by identity rather than treating it as stale.
      if (this.stopRequestGeneration === requestGeneration) {
        this.stopRequestPending = false;
        if (this.globalOperation !== 'scanning' && !this.externalScanning) this.stoppingScan = false;
      }
    }
  }

  private async fetchLatestList(revision = this.listRevisions.next()): Promise<boolean> {
    const capturedStationRevisions = this.gates.snapshotStationRevisions();
    try {
      return this.applyStationList(await GetCurrentStationInfo(), revision, capturedStationRevisions);
    } catch {
      return false;
    }
  }

  private async fetchStationUpdate(
    address: string,
    epoch: number,
    operationRevision: number
  ): Promise<StationInfo | null> {
    const updated = await GetCurrentStationInfo().catch(() => null);
    if (!this.gates.canCommitStationOperation(epoch, address, operationRevision)) return null;
    const station = updated?.find((item) => item.address === address) ?? null;
    if (station) {
      this.mergeStationUpdates([station]);
    }
    return station;
  }

  private powerReadbackLabel(station: StationInfo | null): string {
    if (!station || station.powerState < 0) return t('unavailable');
    return `${t(station.powerFresh ? 'actual' : 'last-known')} ${stateLabel(station)}`;
  }

  private channelReadbackLabel(station: StationInfo | null): string {
    if (!station || station.channel <= 0) return t('unavailable');
    return `${t(station.channelFresh ? 'actual' : 'last-known')} ${station.channel}`;
  }

  async setPower(station: StationInfo, state: PowerTarget) {
    if (!canSetPower(station, state) || this.stationBusy(station.address) || this.gattLockedFor(station.address)) return;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = this.gates.currentScanEpoch;
    const statusOperation = this.gates.beginStatusOperation();
    const operationRevision = this.gates.beginStationOperationRevision(station.address);
    this.setGattBusy(station.address, true);
    this.powerTargetByAddress = { ...this.powerTargetByAddress, [station.address]: state };
    this.powerFeedback.set(station.address, {
      kind: 'pending',
      text: t('Switching to {target}…', { target: targetLabel }),
      target: state
    });
    this.statusMessage = t('Setting {name} to {target}…', { name: station.name, target: targetLabel });
    try {
      const result = await SetStationPower(station.address, state);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.mergeStationUpdates([result.station]);
      this.powerFeedback.set(station.address, result.skipped
        ? {
            kind: 'success', text: t('Already {target}', { target: targetLabel }), target: state,
            readAt: result.station.lastPowerReadAt
          }
        : result.confirmed
        ? {
            kind: 'success', text: t('{target} confirmed', { target: targetLabel }), target: state,
            readAt: result.station.lastPowerReadAt
          }
        : {
            kind: 'warning',
            text: result.confirmationError
              ? t('{target} sent · confirmation failed', { target: targetLabel })
              : t('{target} sent · status unavailable', { target: targetLabel }),
            target: state,
            readAt: result.station.lastPowerReadAt
          });
      if (this.gates.canCommitStatus(statusOperation)) {
        this.statusMessage = result.skipped
          ? t('{name} is already {target}; no command was sent.', { name: station.name, target: targetLabel })
          : result.confirmed
          ? t('{name} is {target}.', { name: station.name, target: targetLabel })
          : result.confirmationError
            ? t('{name}: command sent, but confirmation failed. {detail}', { name: station.name, detail: result.confirmationError })
            : t('{name}: {target} command sent; this firmware cannot confirm the state.', { name: station.name, target: targetLabel });
      }
    } catch (error) {
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      const errorText = String(error);
      const actual = await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.powerFeedback.set(station.address, {
        kind: 'error',
        text: t('Failed · {readback}', { readback: this.powerReadbackLabel(actual) }),
        target: state,
        readAt: actual?.lastPowerReadAt
      });
      if (this.gates.canCommitStatus(statusOperation)) {
        this.statusMessage = `${t('Power change failed for {name}', { name: station.name })}: ${errorText}`;
        pushToast(this.statusMessage);
      }
    } finally {
      if (this.gates.canCleanupStationOperation(station.address, operationRevision)) {
        const nextTargets = { ...this.powerTargetByAddress };
        delete nextTargets[station.address];
        this.powerTargetByAddress = nextTargets;
        this.setGattBusy(station.address, false);
      }
    }
  }

  private actionablePowerStations(state: PowerTarget): StationInfo[] {
    return this.stations.filter((station) => canSetPower(station, state));
  }

  requestBulkPower(state: PowerTarget) {
    // Do not duplicate backend capability/state decisions here. Cached
    // frontend data can be stale after scanning, while the backend refreshes
    // capabilities and returns a result for every known station.
    if (this.bulkLocked || this.actionablePowerStations(state).length === 0) return;
    // When part of the fleet is not fully verified, the command scope is no
    // longer obvious from the button; demand an explicit confirmation that
    // lists what will be affected.
    if (this.untrustedCount > 0) {
      this.ui.requestBulkConfirmation(state);
      return;
    }
    void this.runBulkPower(state);
  }

  async runBulkPower(state: PowerTarget) {
    this.globalOperation = 'bulk-power';
    const statusOperation = this.gates.beginStatusOperation();
    this.bulkTarget = state;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = this.gates.currentScanEpoch;
    this.statusMessage = t('Setting all available stations to {target}…', { target: targetLabel });
    try {
      const result = await SetAllStationsPowerDetailed(state);
      if (!this.gates.canCommitOperation(operationEpoch)) return;
      this.mergeStationUpdates(result.results.map((item) => item.station).filter((item) => Boolean(item?.address)));
      await this.fetchLatestList();
      if (!this.gates.canCommitOperation(operationEpoch)) return;
      for (const item of result.results) {
        const feedback: Pick<PowerFeedback, 'kind' | 'text'> = item.skipped
          ? item.success && item.confirmed
            ? { kind: 'success', text: t('Already {target}', { target: targetLabel }) }
            : { kind: 'warning', text: t('Skipped · {reason}', { reason: item.reason || t('not actionable') }) }
            : item.success && item.confirmed
              ? { kind: 'success', text: t('{target} confirmed', { target: targetLabel }) }
              : item.success && item.commandSent
              ? { kind: 'warning', text: t('{target} sent · {detail}', { target: targetLabel, detail: item.error || t('status unavailable') }) }
              : { kind: 'error', text: item.error || t('Failed to set {target}', { target: targetLabel }) };
        this.powerFeedback.set(item.address, {
          ...feedback,
          target: state,
          readAt: item.station?.lastPowerReadAt
        });
      }
      const summary = summarizeBulkResult(result.results);
      if (!this.gates.canCommitStatus(statusOperation)) return;
      this.statusMessage = formatBulkResult(targetLabel, summary);
      const toastKind = summary.failed.length ? 'error'
        : summary.unconfirmed || summary.skipped ? 'warning'
          : 'success';
      pushToast(`Bulk ${targetLabel}: ${formatBulkResult(targetLabel, summary)}`, toastKind);
    } catch (error) {
      if (!this.gates.canCommitOperation(operationEpoch)) return;
      await this.fetchLatestList();
      if (!this.gates.canCommitOperation(operationEpoch)) return;
      if (this.gates.canCommitStatus(statusOperation)) {
        this.statusMessage = `${t('Bulk {target} operation partially failed', { target: targetLabel })}: ${String(error)}`;
        pushToast(this.statusMessage);
      }
    } finally {
      if (!this.disposed) {
        if (this.globalOperation === 'bulk-power') this.globalOperation = 'idle';
        this.bulkTarget = null;
      }
    }
  }

  startRename(station: StationInfo) {
    if (this.stationBusy(station.address) || this.stationLocked) return;
    this.editingAddress = station.address;
  }

  cancelRename() {
    this.editingAddress = null;
  }

  async saveRename(station: StationInfo, name: string) {
    if (this.stationBusy(station.address) || this.stationLocked) {
      // Keep the row open for a retry and explain why the submission did
      // nothing; a silent rejection leaves the user typing into a dead input.
      const statusOperation = this.gates.beginStatusOperation();
      const reason = t('Rename blocked: another operation is in progress for {name}.', { name: station.name });
      if (this.gates.canCommitStatus(statusOperation)) this.statusMessage = reason;
      pushToast(reason, 'warning');
      return;
    }
    this.cancelRename();
    if (name === station.name) return;
    this.setConfigBusy(station.address, true);
    const statusOperation = this.gates.beginStatusOperation();
    const operationEpoch = this.gates.currentScanEpoch;
    const operationRevision = this.gates.beginStationOperationRevision(station.address);
    try {
      await RenameStationByAddress(station.address, name);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.stations = this.stations.map((current) => current.address === station.address
        ? this.withStationChanges(current, { name: name || current.originalName })
        : current);
      if (this.gates.canCommitStatus(statusOperation)) this.statusMessage = name ? t('Renamed to {name}.', { name }) : t('Reset name for {name}.', { name: station.originalName });
    } catch (error) {
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.gates.canCommitStatus(statusOperation)) {
        this.statusMessage = withDetail('Error renaming', error);
        pushToast(this.statusMessage);
      }
    } finally {
      this.apiStatus.refresh();
      if (this.gates.canCleanupStationOperation(station.address, operationRevision)) {
        this.setConfigBusy(station.address, false);
      }
    }
  }

  async identify(station: StationInfo) {
    if (this.stationBusy(station.address) || this.gattLockedFor(station.address)) return;
    this.setGattBusy(station.address, true);
    const statusOperation = this.gates.beginStatusOperation();
    const operationEpoch = this.gates.currentScanEpoch;
    const operationRevision = this.gates.beginStationOperationRevision(station.address);
    try {
      await IdentifyStation(station.address);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.gates.canCommitStatus(statusOperation)) this.statusMessage = t('Identify signal sent to {name}.', { name: station.name });
    } catch (error) {
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.gates.canCommitStatus(statusOperation)) {
        this.statusMessage = `${t('Identify failed for {name}', { name: station.name })}: ${String(error)}`;
        pushToast(this.statusMessage);
      }
    } finally {
      if (this.gates.canCleanupStationOperation(station.address, operationRevision)) {
        this.setGattBusy(station.address, false);
      }
    }
  }

  async refreshCapabilities(station: StationInfo) {
    if (this.stationBusy(station.address) || this.gattLockedFor(station.address)) return;
    this.setGattBusy(station.address, true);
    const statusOperation = this.gates.beginStatusOperation();
    const operationEpoch = this.gates.currentScanEpoch;
    const operationRevision = this.gates.beginStationOperationRevision(station.address);
    try {
      const updated = await RefreshStationCapabilities(station.address);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.stations = this.stations.map((current) => current.address === station.address ? updated : current);
      if (this.gates.canCommitStatus(statusOperation)) {
        const message = updated.lastError
          ? `${t('Capabilities refreshed for {name}, but some values are unavailable', { name: station.name })}: ${updated.lastError}`
          : t('Capabilities refreshed for {name}.', { name: station.name });
        this.statusMessage = message;
        if (updated.lastError) pushToast(message, 'warning');
      }
    } catch (error) {
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.gates.canCommitStatus(statusOperation)) {
        this.statusMessage = `${t('Capability refresh failed for {name}', { name: station.name })}: ${String(error)}`;
        pushToast(this.statusMessage);
      }
    } finally {
      if (this.gates.canCleanupStationOperation(station.address, operationRevision)) {
        this.setGattBusy(station.address, false);
      }
    }
  }

  clearChannelEditorFeedback() {
    this.channelError = '';
    this.channelWarning = false;
  }

  resetLocalizedMessages() {
    this.statusMessage = t('Ready to scan.');
    this.powerFeedback.clearAll();
  }

  async saveChannel(station: StationInfo, targetChannel: number, allowUnknownConflictRisk: boolean) {
    if (!station) return;
    const blockedReason = channelChangeBlockedReason(station);
    if (blockedReason) {
      this.channelError = blockedReason;
      this.channelWarning = true;
      return;
    }
    if (this.stationBusy(station.address) || this.gattLockedFor(station.address) ||
      (hasCurrentChannel(station) && station.channel === targetChannel)) return;
    const address = station.address;
    // Capture the display name: the station can drop out of the list while
    // the write is in flight, and the post-await status messages must not
    // dereference a null selection.
    const stationName = station.name;
    const statusOperation = this.gates.beginStatusOperation();
    const operationEpoch = this.gates.currentScanEpoch;
    const operationRevision = this.gates.beginStationOperationRevision(address);
    this.setGattBusy(address, true);
    this.channelError = '';
    this.channelWarning = false;
    try {
      const result = await SetStationChannel(address, targetChannel, allowUnknownConflictRisk);
      if (!this.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      let actual = result.station?.address ? result.station : null;
      if (actual) {
        this.mergeStationUpdates([actual]);
      } else {
        actual = await this.fetchStationUpdate(address, operationEpoch, operationRevision);
        if (!this.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      }
      if (result.confirmed === false) {
        const warning = result.confirmationError || 'Channel readback is unavailable.';
        this.channelError = `${t('Channel command sent but unconfirmed')}: ${warning} ${t('Readback')}: ${this.channelReadbackLabel(actual)}.`;
        this.channelWarning = true;
        if (this.gates.canCommitStatus(statusOperation)) this.statusMessage = t('{name}: channel command sent, but confirmation failed. {detail}', { name: stationName, detail: warning });
        return;
      }
      if (!actual) {
        this.commitStations(this.stations.map((item) => item.address === address
          ? this.withStationChanges(item, { channel: result.channel })
          : item));
      }
      this.ui.closeChannelEditor();
      if (this.gates.canCommitStatus(statusOperation)) this.statusMessage = result.commandSent
        ? t('Channel changed from {previous} to {channel}. {warnings}', { previous: result.previousChannel || t('unknown'), channel: result.channel, warnings: result.warnings.join(' ') })
        : t('Channel already set to {channel}; no command was sent. {warnings}', { channel: result.channel, warnings: result.warnings.join(' ') });
    } catch (error) {
      if (!this.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      const actual = await this.fetchStationUpdate(address, operationEpoch, operationRevision);
      if (!this.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      this.channelError = `${String(error)} ${t('Readback')}: ${this.channelReadbackLabel(actual)}.`;
      this.channelWarning = false;
      if (this.gates.canCommitStatus(statusOperation)) {
        this.statusMessage = `${t('Channel change failed')}: ${this.channelError}`;
        pushToast(this.statusMessage);
      }
    } finally {
      if (this.gates.canCleanupStationOperation(address, operationRevision)) {
        this.setGattBusy(address, false);
      }
    }
  }

  private handleAutoSleepEvent(event: AutoSleepEvent) {
    if (this.disposed || !event) return;
    if (event.phase !== 'started' && Array.isArray(event.stations)) {
      this.applyExternalStationUpdate(event.updateId ?? 0, event.stations);
    }
    switch (event.phase) {
      case 'started':
        this.autoSleepRunning = true;
        // This lifecycle now owns the global status line. Invalidate a status
        // refresh that began before automatic sleep acquired the backend.
        this.gates.beginStatusOperation();
        this.statusMessage = t('Auto sleep: scanning and putting all stations to sleep...');
        pushToast(t('Session ended — scanning and putting all stations to sleep.'), 'info');
        break;
      case 'completed': {
        this.autoSleepRunning = false;
        this.gates.beginStatusOperation();
        const success = event.success ?? 0;
        const failed = event.failed ?? 0;
        const skipped = event.skipped ?? 0;
        this.statusMessage = t('Auto sleep finished: {success} succeeded, {failed} failed, {skipped} skipped.', { success, failed, skipped });
        if (failed > 0) {
          pushToast(skipped
            ? t('Auto sleep finished: {success} succeeded, {failed} failed, {skipped} skipped.', { success, failed, skipped })
            : t('Auto sleep finished: {success} succeeded, {failed} failed.', { success, failed }), 'warning');
        } else if (skipped > 0) {
          pushToast(t('Auto sleep finished: {success} succeeded, {skipped} skipped.', { success, skipped }), 'warning');
        } else {
          pushToast(t('Auto sleep finished: {success} station(s) put to sleep.', { success }), 'success');
        }
        break;
      }
      case 'cancelled': {
        this.autoSleepRunning = false;
        this.gates.beginStatusOperation();
        const success = event.success ?? 0;
        const failed = event.failed ?? 0;
        const skipped = event.skipped ?? 0;
        const details = success || failed || skipped
          ? t('{success} succeeded, {failed} failed, {skipped} skipped', { success, failed, skipped })
          : t('no station commands completed');
        this.statusMessage = t('Auto sleep cancelled: {details}.', { details });
        pushToast(this.statusMessage, success || failed ? 'warning' : 'info');
        break;
      }
      case 'skipped':
        this.autoSleepRunning = false;
        this.gates.beginStatusOperation();
        this.statusMessage = `${t('Auto sleep skipped')}: ${event.error || t('Bluetooth busy')}.`;
        pushToast(this.statusMessage, 'info');
        break;
      case 'failed':
        this.autoSleepRunning = false;
        this.gates.beginStatusOperation();
        this.statusMessage = `${t('Auto sleep failed')}: ${event.error || t('unknown error')}.`;
        pushToast(this.statusMessage);
        break;
    }
  }

  private handleExternalStationUpdate(event: ExternalStationUpdateEvent) {
    if (this.disposed || !event || !Array.isArray(event.stations)) return;
    this.applyExternalStationUpdate(event.id, event.stations);
  }

  private applyExternalStationUpdate(id: number, stations: StationInfo[]) {
    if (id > 0 && id <= this.lastExternalUpdateId) return;
    if (id > 0) this.lastExternalUpdateId = id;
    // Invalidate any list request that began before this event. Without this,
    // a delayed periodic status response can reapply the snapshot it captured
    // before the HTTP or automatic-sleep operation completed.
    this.listRevisions.next();
    // A local operation owns its station snapshot until its promise settles.
    // Skipped updates are recovered by that result or the periodic poll.
    const safeUpdates = stations.filter((station) =>
      Boolean(station?.address) && !this.stationBusy(station.address)
    );
    this.mergeStationUpdates(safeUpdates);
  }
}
