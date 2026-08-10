import { EventsOn } from '../../../wailsjs/runtime/runtime';
import {
  GetCurrentStationInfo,
  GetScanOnStartup,
  GetScanStatus,
  GetStatusPollIntervalSeconds,
  GetStatusPollingEnabled,
  IsScanning
} from '../backend';
import type { PowerFeedback, PowerTarget, StationInfo } from '../types';
import { scanErrorCopy, type ScanErrorInfo } from '../scan-error';
import { pushToast } from '../toast';
import { deriveOperationLocks, type GlobalOperation } from '../operation-state';
import { ExternalScanCoordinator, type ExternalScanEvent } from '../external-scan';
import { OperationGate } from '../operation-gate';
import { PowerFeedbackRegistry } from '../power-feedback';
import { RevisionGate } from '../revision-gate';
import { ScanTimer } from '../scan-timer';
import { ApiStatusPoller } from '../api-status-poller';
import { t } from '../i18n.svelte';
import { AutoSleepEventCoordinator, type AutoSleepEvent } from './auto-sleep-events';
import {
  ExternalStationUpdateCoordinator,
  type ExternalStationUpdateEvent
} from './external-station-updates';
import { FleetState } from './fleet-state.svelte';
import { StationActionController } from './station-actions';
import { StationScanController } from './station-scan-controller';

const DEFAULT_STATUS_POLL_INTERVAL_SECONDS = 15;
const MIN_STATUS_POLL_INTERVAL_SECONDS = 5;
const MAX_STATUS_POLL_INTERVAL_SECONDS = 300;

// Overlay controls owned by App. The store calls back into them whenever an
// operation must close or open a layer instead of reaching into UI state.
export interface StationStoreUi {
  closeChannelEditor(): void;
  forceCloseChannelEditor(): void;
  clearBulkConfirmation(): void;
  requestBulkConfirmation(target: PowerTarget): void;
}

// All fleet state and backend orchestration. Held in one rune-backed class so
// App.svelte stays a pure composition + overlay-switching shell. Effects are
// deliberately absent here ($effect cannot run outside components); App wires
// the two render-cycle hooks via syncChannelMemory() and explicit calls.
export class StationStore {
  private fleet = new FleetState();
  statusMessage = $state(t('Ready to scan.'));
  gattOperations = $state(new Set<string>());
  configOperations = $state(new Set<string>());
  powerTargetByAddress = $state<Record<string, PowerTarget | undefined>>({});
  powerFeedbackMap = $state<Record<string, PowerFeedback | undefined>>({});
  globalOperation = $state<GlobalOperation>('idle');
  bulkTarget = $state<PowerTarget | null>(null);
  cancellingBulk = $state(false);
  editingAddress = $state<string | null>(null);
  channelError = $state('');
  channelWarning = $state(false);
  channelSavingAddress = $state<string | null>(null);
  scanError = $state<ScanErrorInfo | null>(null);
  externalScanning = $state(false);
  externalOperationRunning = $state(false);
  autoSleepRunning = $state(false);
  stoppingScan = $state(false);
  scanElapsed = $state(0);
  apiRunning = $state(false);
  apiAddress = $state('');
  apiError = $state('');
  configWarnings = $state<string[]>([]);
  configWritable = $state(true);

  private statusCheckInterval: ReturnType<typeof setInterval> | null = null;
  private statusPollIntervalSeconds = DEFAULT_STATUS_POLL_INTERVAL_SECONDS;
  private statusPollingEnabled = true;
  private apiStatusStarted = false;
  private startupAPIStatusReady: Promise<void> = Promise.resolve();
  private cancelExternalScanListener: (() => void) | null = null;
  private cancelExternalScanFailureListener: (() => void) | null = null;
  private cancelExternalScanStartedListener: (() => void) | null = null;
  private cancelExternalScanCancelledListener: (() => void) | null = null;
  private cancelAutoSleepListener: (() => void) | null = null;
  private cancelExternalStationUpdateListener: (() => void) | null = null;
  private cancelExternalOperationListener: (() => void) | null = null;
  private externalOperationIds = new Set<number>();
  private externalOperationRevision = 0;
  stopRequestPending = false;
  stopRequestGeneration = 0;
  disposed = false;
  startupPending = true;

  readonly powerFeedback = new PowerFeedbackRegistry((next) => {
    this.powerFeedbackMap = next;
  });
  private scanTimer = new ScanTimer((seconds) => {
    this.scanElapsed = seconds;
  });
  readonly listRevisions = new RevisionGate();
  readonly gates = new OperationGate();


  // Coordination for scans started outside this UI (the HTTP API). All host
  // callbacks write through store state so Svelte reactivity keeps the
  // template in sync.
  readonly externalScan = new ExternalScanCoordinator({
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
  readonly apiStatus = new ApiStatusPoller({
    isDisposed: () => this.disposed,
    commitStatus: (status) => {
      this.apiRunning = status.running;
      this.apiError = status.error;
      this.apiAddress = status.address;
      this.configWarnings = status.warnings ?? [];
      this.configWritable = status.configWritable ?? true;
      if (Array.isArray(status.activeOperations)) {
        this.reconcileExternalOperations(status.activeOperations, status.operationRevision ?? 0);
      }
    },
    commitFailure: (error) => {
      this.apiRunning = false;
      this.apiError = error;
    },
    reportConfigWarning: (warning) => pushToast(warning, 'warning')
  });

  private externalStationUpdates = new ExternalStationUpdateCoordinator({
    isDisposed: () => this.disposed,
    isStationBusy: (address) => this.stationBusy(address),
    invalidatePendingLists: () => { this.listRevisions.next(); },
    mergeStations: (stations) => this.mergeStationUpdates(stations)
  });

  private autoSleepEvents = new AutoSleepEventCoordinator({
    isDisposed: () => this.disposed,
    setRunning: (running) => { this.autoSleepRunning = running; },
    beginStatusOperation: () => { this.gates.beginStatusOperation(); },
    setStatusMessage: (message) => { this.statusMessage = message; },
    applyStations: (updateId, stations) => this.externalStationUpdates.apply(updateId, stations)
  });

  private actions: StationActionController;
  private scans: StationScanController;

  constructor(readonly ui: StationStoreUi) {
    this.actions = new StationActionController(this);
    this.scans = new StationScanController(this);
  }

  get stations(): StationInfo[] { return this.fleet.stations; }
  set stations(stations: StationInfo[]) { this.fleet.replace(stations); }
  get sortedStations(): StationInfo[] { return this.fleet.sortedStations; }
  get conflictDetails(): string { return this.fleet.conflictDetails; }
  get fleetOn(): number { return this.fleet.fleetOn; }
  get fleetStandby(): number { return this.fleet.fleetStandby; }
  get fleetSleep(): number { return this.fleet.fleetSleep; }
  get fleetUnverified(): number { return this.fleet.fleetUnverified; }
  get actionableOn(): StationInfo[] { return this.fleet.actionableOn; }
  get actionableStandby(): StationInfo[] { return this.fleet.actionableStandby; }
  get actionableSleep(): StationInfo[] { return this.fleet.actionableSleep; }
  get allOn(): boolean { return this.fleet.allOn; }
  get allStandby(): boolean { return this.fleet.allStandby; }
  get allSleep(): boolean { return this.fleet.allSleep; }
  get visibleCount(): number { return this.fleet.visibleCount; }
  get invisibleCount(): number { return this.fleet.invisibleCount; }
  get uncertainCount(): number { return this.fleet.uncertainCount; }
  get untrustedCount(): number { return this.fleet.untrustedCount; }

  displayChannel(station: StationInfo): number {
    return this.fleet.displayChannel(station);
  }

  syncChannelMemory() {
    this.fleet.syncChannelMemory();
  }

  onLocaleChanged() {
    // Transient text is stored as rendered copy. Drop per-device feedback and
    // rebuild the status line from the current operation so no old-language
    // fragments survive an immediate locale switch.
    this.powerFeedback.clearAll();
    this.channelError = '';
    this.channelWarning = false;
    if (this.stoppingScan) {
      this.statusMessage = t('Stopping scan...');
    } else if (this.cancellingBulk) {
      this.statusMessage = t('Stopping bulk power...');
    } else if (this.globalOperation === 'scanning') {
      this.statusMessage = t('Scanning for base stations...');
    } else if (this.externalScanning) {
      this.statusMessage = t('External scan in progress...');
    } else if (this.externalOperationRunning) {
      this.statusMessage = t('Bluetooth operation in progress');
    } else if (this.autoSleepRunning) {
      this.statusMessage = t('Auto sleep: scanning and putting all stations to sleep...');
    } else if (this.globalOperation === 'bulk-power' && this.bulkTarget) {
      this.statusMessage = t('Setting all available stations to {target}…', {
        target: t(this.bulkTarget === 'on' ? 'On' : this.bulkTarget === 'standby' ? 'Standby' : 'Sleep')
      });
    } else if (this.globalOperation === 'status-refresh') {
      this.statusMessage = t('Reading station states...');
    } else if (this.scanError) {
      // The scan-failure card is still on screen; rebuilding its status line
      // keeps the two consistent instead of reverting to "Ready to scan.".
      this.statusMessage = this.scanError.kind === 'unknown'
        ? t('Scan failed.')
        : t('Scan failed: {heading}', { heading: scanErrorCopy(this.scanError).heading });
    } else {
      this.statusMessage = t('Ready to scan.');
    }
  }
  private operationLocks = $derived(deriveOperationLocks({
    global: this.globalOperation,
    externalScanning: this.externalScanning,
    externalOperationRunning: this.externalOperationRunning,
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
  anyDeviceOperation = $derived(this.operationLocks.anyDeviceOperation);
  scanLocked = $derived(this.operationLocks.scanLocked);
  bulkLocked = $derived(this.operationLocks.bulkLocked);
  stationLocked = $derived(this.operationLocks.stationLocked);
  // The periodic status reader occupies one of the backend's two device
  // operation slots. Keep the remaining slot available to one foreground
  // action instead of allowing a second action that will fail with Busy.
  private foregroundGattCapacity = $derived(this.isStatusChecking ? 1 : 2);
  private gattCapacityReached = $derived(this.gattOperations.size >= this.foregroundGattCapacity);

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

  occupiedChannelsExcluding(selectedAddress: string | null): Map<number, string[]> {
    return this.fleet.occupiedChannelsExcluding(selectedAddress);
  }

  hasUnknownVisibleChannelExcluding(selectedAddress: string | null): boolean {
    return this.fleet.hasUnknownVisibleChannelExcluding(selectedAddress);
  }
  stationBusy(address: string): boolean {
    return this.gattOperations.has(address) || this.configOperations.has(address);
  }

  configBusy(address: string): boolean {
    return this.configOperations.has(address);
  }

  gattLockedFor(address: string): boolean {
    return this.stationLocked || (this.gattCapacityReached && !this.gattOperations.has(address));
  }

  beginScanTimer() {
    this.scanTimer.begin();
  }

  maybeEndScanTimer() {
    // A local scan and an external scan can overlap; the timer belongs to
    // whichever is still running, so only stop when both have finished.
    if (this.globalOperation !== 'scanning' && !this.externalScanning) this.scanTimer.end();
  }

  setGattBusy(address: string, busy: boolean) {
    const next = new Set(this.gattOperations);
    if (busy) next.add(address);
    else next.delete(address);
    this.gattOperations = next;
  }

  setConfigBusy(address: string, busy: boolean) {
    const next = new Set(this.configOperations);
    if (busy) next.add(address);
    else next.delete(address);
    this.configOperations = next;
  }

  commitStations(updated: StationInfo[]) {
    this.fleet.commit(updated);
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

  prepareForScan(clearOperations = true) {
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
      this.channelSavingAddress = null;
      this.gates.clearOperationTokens();
    }
    this.powerTargetByAddress = {};
    this.powerFeedback.clearAll();
  }

  mergeStationUpdates(updated: StationInfo[]) {
    if (!updated.length) return;
    this.fleet.merge(updated);
    this.powerFeedback.reconcile(this.stations);
  }

  private reconcileExternalOperations(operations: Array<{ id: number; kind?: string }>, revision: number) {
    if (revision <= 0 && this.externalOperationRevision > 0) return;
    if (revision > 0 && revision < this.externalOperationRevision) return;
    if (revision > 0) this.externalOperationRevision = revision;
    this.externalOperationIds = new Set(
      operations.map((operation) => Number(operation.id)).filter((id) => Number.isFinite(id))
    );
    this.externalOperationRunning = this.externalOperationIds.size > 0;
  }

  setStatusPollIntervalSeconds(intervalSeconds: number) {
    if (this.disposed || !Number.isFinite(intervalSeconds)) return;
    const next = Math.min(
      MAX_STATUS_POLL_INTERVAL_SECONDS,
      Math.max(MIN_STATUS_POLL_INTERVAL_SECONDS, Math.round(intervalSeconds))
    );
    // Unchanged interval: only the very first call must fall through so the
    // health poller is started; with station polling disabled the interval
    // timer is null, which used to trigger a redundant health request here.
    if (next === this.statusPollIntervalSeconds && this.apiStatusStarted) return;
    this.statusPollIntervalSeconds = next;
    this.apiStatusStarted = true;

    if (this.statusCheckInterval) clearInterval(this.statusCheckInterval);
    this.statusCheckInterval = null;
    const intervalMs = next * 1000;
    this.startupAPIStatusReady = this.apiStatus.start(intervalMs);
    if (this.statusPollingEnabled) {
      this.statusCheckInterval = setInterval(() => {
        void this.periodicStatusCheck();
      }, intervalMs);
    }
  }

  setStatusPollingEnabled(enabled: boolean) {
    if (this.disposed || this.statusPollingEnabled === enabled) return;
    this.statusPollingEnabled = enabled;
    if (this.statusCheckInterval) clearInterval(this.statusCheckInterval);
    this.statusCheckInterval = null;
    if (enabled) {
      const intervalMs = this.statusPollIntervalSeconds * 1000;
      this.statusCheckInterval = setInterval(() => {
        void this.periodicStatusCheck();
      }, intervalMs);
    }
  }

  withStationChanges(current: StationInfo, changes: Partial<StationInfo>): StationInfo {
    return this.fleet.patch(current, changes);
  }

  mount() {
    const startupScanEpoch = this.gates.currentScanEpoch;
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
      this.autoSleepEvents.handle(value as AutoSleepEvent);
    });
    this.cancelExternalStationUpdateListener = EventsOn('external-stations-updated', (value: unknown) => {
      this.externalStationUpdates.handle(value as ExternalStationUpdateEvent);
    });
    this.cancelExternalOperationListener = EventsOn('external-operation', (value: unknown) => {
      const event = value as { id?: number; phase?: 'started' | 'finished'; revision?: number };
      if (this.disposed || !Number.isFinite(event?.id)) return;
      const id = Number(event.id);
      const revision = Number(event.revision ?? 0);
      if (revision > 0 && revision <= this.externalOperationRevision) return;
      if (revision > 0) this.externalOperationRevision = revision;
      if (event.phase === 'started') this.externalOperationIds.add(id);
      else if (event.phase === 'finished') this.externalOperationIds.delete(id);
      else return;
      this.externalOperationRunning = this.externalOperationIds.size > 0;
    });
    // Register operation listeners before the first asynchronous health poll
    // so a newly started HTTP action cannot slip through the mount window.
    this.setStatusPollIntervalSeconds(DEFAULT_STATUS_POLL_INTERVAL_SECONDS);
    const startupAPIStatusReady = this.startupAPIStatusReady;
    void GetStatusPollIntervalSeconds()
      .then((intervalSeconds) => this.setStatusPollIntervalSeconds(intervalSeconds))
      .catch(() => {
        // Keep the default interval when the persisted preference cannot be read.
      });
    void GetStatusPollingEnabled()
      .then((enabled) => this.setStatusPollingEnabled(enabled))
      .catch(() => {
        // Keep automatic station refresh enabled when the preference is unreadable.
      });
    void (async () => {
      const [startupScanning, scanOnStartup] = await Promise.all([
        IsScanning().catch(() => false),
        // Fail closed: when the persisted preference is unreadable, skipping
        // the scan honors a disabled setting instead of triggering a scan the
        // user explicitly turned off.
        GetScanOnStartup().catch(() => false),
        // Wait for the operation-health snapshot started above. It recovers
        // automatic sleep (and HTTP work) that began before event listeners
        // mounted, closing the only window where an internal scan could be
        // misidentified as a user-stoppable external scan.
        startupAPIStatusReady
      ]);
      // Do not allow the first polling tick to acquire the backend operation
      // lock before the initial external-scan check can start the local scan.
      this.startupPending = false;
      // An external scan event may have arrived while this initial query was
      // pending. Do not let its older result overwrite the newer event state.
      if (this.disposed || startupScanEpoch !== this.gates.currentScanEpoch) return;
      if (startupScanning) {
        // An auto-sleep scan is internal; adopting it would expose Stop for
        // it. Skip adoption and let the periodic check take over afterwards.
        if (!this.autoSleepRunning && !this.externalOperationRunning) {
          await this.externalScan.adoptUnknown();
        }
      } else if (scanOnStartup) {
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
    this.fleet.stopChannelMemoryExpiry();
    if (this.statusCheckInterval) clearInterval(this.statusCheckInterval);
    this.powerFeedback.clearAll();
    this.cancelExternalScanListener?.();
    this.cancelExternalScanFailureListener?.();
    this.cancelExternalScanStartedListener?.();
    this.cancelExternalScanCancelledListener?.();
    this.cancelAutoSleepListener?.();
    this.cancelExternalStationUpdateListener?.();
    this.cancelExternalOperationListener?.();
    this.externalOperationIds.clear();
    this.externalOperationRunning = false;
  }

  private periodicStatusCheck() {
    return this.scans.periodicStatusCheck();
  }

  startScan() {
    return this.scans.startScan();
  }

  stopScan() {
    return this.scans.stopScan();
  }

  setPower(station: StationInfo, state: PowerTarget) {
    return this.actions.setPower(station, state);
  }

  requestBulkPower(state: PowerTarget) {
    return this.actions.requestBulkPower(state);
  }

  runBulkPower(state: PowerTarget) {
    return this.actions.runBulkPower(state);
  }

  cancelBulkPower() {
    return this.actions.cancelBulkPower();
  }

  startRename(station: StationInfo) {
    return this.actions.startRename(station);
  }

  cancelRename() {
    return this.actions.cancelRename();
  }

  saveRename(station: StationInfo, name: string) {
    return this.actions.saveRename(station, name);
  }

  identify(station: StationInfo) {
    return this.actions.identify(station);
  }

  refreshCapabilities(station: StationInfo) {
    return this.actions.refreshCapabilities(station);
  }

  clearChannelEditorFeedback() {
    return this.actions.clearChannelEditorFeedback();
  }

  saveChannel(station: StationInfo, targetChannel: number, allowUnknownConflictRisk: boolean) {
    return this.actions.saveChannel(station, targetChannel, allowUnknownConflictRisk);
  }
}
