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
import { backendCopy } from '../backend-copy';
import { isTerminalScanState } from '../result-format';
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
const PROJECTION_REFRESH_RETRY_MS = 250;
const PROJECTION_REFRESH_MAX_RETRY_MS = 5000;
// Bound the exponential failure loop so a persistently rejecting backend read
// cannot keep a fast retry timer alive for the whole session. An explicit
// settings save resets the backoff; successful API-health polls only allow one
// half-open probe until the cached projection can be read again.
const PROJECTION_REFRESH_GIVE_UP_AFTER = 6;

// Overlay controls owned by App. The store calls back into them whenever an
// operation must close or open a layer instead of reaching into UI state.
export interface StationStoreUi {
  closeChannelEditor(): void;
  forceCloseChannelEditor(): void;
  clearBulkConfirmation(): void;
  requestBulkConfirmation(target: PowerTarget): void;
}

// A startup scan deferred by a boot-time lock normally retries when the lock's
// terminal event or snapshot arrives. A lost event leaves the health poll as the
// only retry path, and its interval is user-tunable up to several minutes; a
// dedicated short timer keeps the configured startup scan from stalling that long.
const DEFERRED_STARTUP_SCAN_RETRY_MS = 3000;

// A hung backend getter (Wails bindings carry no timeout of their own) must
// not keep startupPending true for the whole session; after this window the
// store proceeds as if no scan is running and lets the periodic poll
// reconcile the real state.
const STARTUP_PROBE_TIMEOUT_MS = 15000;

// Consecutive health-probe failures after which scan ownership is treated as
// decidable even though no health snapshot committed. A failing probe means
// the HTTP listener is unreachable, so HTTP-driven external operations cannot
// be in flight; auto-sleep state arrives over the separate Wails event stream,
// and a deferred startup scan that loses a race still re-arms on the next
// retry. Without this fallback a persistently failing probe leaves ownership
// permanently unknown, stalling the configured startup scan and its retry
// timer for the whole session.
const STARTUP_OWNERSHIP_FAILURE_THRESHOLD = 3;

// Resolves with the promise's value, or with the fallback once the timeout
// elapses. Startup probes use it so a single hung backend getter cannot
// freeze the UI state machine.
function resolveWithTimeout<T>(promise: Promise<T>, timeoutMs: number, fallback: T): Promise<T> {
  return new Promise<T>((resolve) => {
    const timer = setTimeout(() => resolve(fallback), timeoutMs);
    void promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      () => {
        clearTimeout(timer);
        resolve(fallback);
      }
    );
  });
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
  private projectionRefreshTimer: ReturnType<typeof setTimeout> | null = null;
  private projectionRefreshPending = false;
  private projectionRefreshInFlight = false;
  private projectionRefreshFailures = 0;
  private statusPollIntervalSeconds = DEFAULT_STATUS_POLL_INTERVAL_SECONDS;
  private statusPollingEnabled = true;
  // Re-enabling status polling promises an immediate refresh, but the startup
  // barrier gates every status check until it completes. When the re-enable
  // lands inside that window the promised refresh is owed and runs as soon as
  // the barrier clears instead of waiting a full poll interval.
  private owedImmediateStatusCheck = false;
  private scanOnStartupEnabled = false;
  private scanOnStartupPreferenceRevision = 0;
  private statusPollIntervalPreferenceRevision = 0;
  private statusPollingEnabledPreferenceRevision = 0;
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
  // A scan-on-startup was configured but blocked by a lock (auto-sleep or an
  // external HTTP operation already running at boot). Retry it once the lock
  // releases instead of skipping the configured startup scan for the session.
  private startupScanDeferred = false;
  private deferredStartupScanTimer: ReturnType<typeof setInterval> | null = null;
  // The first API health snapshot tells which running operations are internal
  // (auto-sleep or HTTP) and which are user-stoppable external scans. Until it
  // arrives — notably when the startup barrier timed out — unknown scans must
  // not be adopted and deferred startup scans must not start, or an internal
  // scan could be exposed as stoppable or rejected as busy.
  private startupScanOwnershipKnown = false;
  // Counts consecutive health-probe failures so a persistently unreachable
  // API can still settle scan ownership instead of leaving it unknown forever
  // (which stalls the deferred startup scan for the session). Reset by a
  // committed snapshot.
  private healthProbeFailures = 0;
  private mounted = false;

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
    canAdoptUnknownScan: () => this.startupScanOwnershipKnown &&
      !this.autoSleepRunning && !this.externalOperationRunning &&
      !this.isLoading && !this.isBulkLoading && !this.anyDeviceOperation &&
      !this.stoppingScan && !this.stopRequestPending,
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
    restartScanTimer: () => this.restartScanTimer(),
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
      // The first committed health snapshot makes scan ownership decidable:
      // auto-sleep and HTTP operations become distinguishable from unknown
      // external scans, releasing the adoption/deferred-scan startup gates.
      this.startupScanOwnershipKnown = true;
      this.healthProbeFailures = 0;
      this.apiRunning = status.running;
      this.apiError = status.error;
      this.apiAddress = status.address;
      this.configWarnings = status.warnings ?? [];
      this.configWritable = status.configWritable ?? true;
      if (Array.isArray(status.activeOperations)) {
        this.reconcileExternalOperations(status.activeOperations, status.operationRevision ?? 0);
      }
      // Disabling automatic station polling must not freeze time-derived
      // freshness flags forever. Re-project the cached snapshots alongside
      // the lightweight API-health poll without performing any Bluetooth I/O.
      if (!this.statusPollingEnabled) void this.refreshStationProjectionAfterHealthCheck();
    },
    commitFailure: (error) => {
      this.apiRunning = false;
      this.apiError = error;
      // A failing health probe means the HTTP listener is unreachable, so no
      // HTTP-driven external operation can be in flight and ownership becomes
      // decidable even without a committed snapshot. Treat it as such after a
      // short failure streak; otherwise a persistently failing probe leaves
      // ownership unknown and stalls the deferred startup scan for the session.
      this.healthProbeFailures += 1;
      if (this.healthProbeFailures < STARTUP_OWNERSHIP_FAILURE_THRESHOLD) return;
      this.startupScanOwnershipKnown = true;
      // The same persistent failure must also release the operation locks a
      // lost terminal event (or a missed snapshot) left armed: while the
      // backend is unreachable no external operation can be in flight, and
      // keeping the flags would lock the scan/bulk controls and skip the
      // periodic status check for the rest of the session. Auto-sleep is NOT
      // fed an idle observation here: a failed probe carries no evidence that
      // the in-process auto-sleep action settled, and counting failures as
      // idle snapshots could clear the busy flag while a real action still
      // runs, exposing its internal scan for adoption. Healthy snapshots keep
      // reconciling auto-sleep through reconcileExternalOperations.
      if (this.externalOperationRunning || this.externalOperationIds.size > 0) {
        this.externalOperationIds.clear();
        this.externalOperationRunning = false;
      }
      this.maybeRunDeferredStartupScan();
    },
    reportConfigWarning: (warning) => pushToast(backendCopy(warning), 'warning')
  });

  private externalStationUpdates = new ExternalStationUpdateCoordinator({
    isDisposed: () => this.disposed,
    localListOperationOwnsSnapshot: () =>
      this.globalOperation === 'scanning' || this.globalOperation === 'bulk-power',
    isStationBusy: (address) => this.stationBusy(address),
    invalidatePendingLists: () => { this.listRevisions.next(); },
    mergeStations: (stations) => this.mergeStationUpdates(stations)
  });

  private autoSleepEvents = new AutoSleepEventCoordinator({
    isDisposed: () => this.disposed,
    setRunning: (running) => {
      this.autoSleepRunning = running;
      if (!running) this.maybeRunDeferredStartupScan();
    },
    beginStatusOperation: () => { this.gates.beginStatusOperation(); },
    setStatusMessage: (message) => { this.statusMessage = message; },
    applyStations: (updateId, stations) => this.externalStationUpdates.apply(updateId, stations),
    // A periodic status refresh is not interactive and does not write a
    // terminal summary on success, so it does not own the line here.
    foregroundOwnsStatusLine: () => this.globalOperation === 'scanning' ||
      this.globalOperation === 'bulk-power' || this.stoppingScan || this.cancellingBulk
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
  get channelDisplayByAddress(): ReadonlyMap<string, number> { return this.fleet.channelDisplayByAddress; }
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
    } else if (this.autoSleepRunning) {
      this.statusMessage = t('Auto sleep: scanning and putting all stations to sleep...');
    } else if (this.externalOperationRunning) {
      this.statusMessage = t('Bluetooth operation in progress');
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
  bulkLocked = $derived(this.operationLocks.bulkLocked || this.cancellingBulk);
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

  restartScanTimer() {
    this.scanTimer.restart();
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
      this.powerTargetByAddress = {};
      this.powerFeedback.clearAll();
      return;
    }
    // An external scan starts while single-station operations are still in
    // flight. Their busy flags are preserved above, so keep their pending
    // target highlight and feedback notes too: they settle through the gated
    // commit path and would otherwise disappear with no result ever written
    // for the operation. Entries whose operation already ended are dropped.
    const activeAddresses = this.gattOperations;
    const nextTargets: Record<string, PowerTarget | undefined> = {};
    for (const [address, target] of Object.entries(this.powerTargetByAddress)) {
      if (activeAddresses.has(address)) nextTargets[address] = target;
    }
    this.powerTargetByAddress = nextTargets;
    this.powerFeedback.retain(activeAddresses);
  }

  mergeStationUpdates(updated: StationInfo[]) {
    if (!updated.length) return;
    this.fleet.merge(updated);
    this.powerFeedback.reconcile(this.stations);
  }

  private projectionRefreshBlocked() {
    return this.startupPending || this.globalOperation !== 'idle' ||
      this.externalScanning || this.autoSleepRunning || this.externalOperationRunning ||
      this.anyDeviceOperation;
  }

  private scheduleProjectionRefresh() {
    if (this.disposed || this.projectionRefreshTimer !== null) return;
    const retryMs = Math.min(
      PROJECTION_REFRESH_MAX_RETRY_MS,
      PROJECTION_REFRESH_RETRY_MS * (2 ** Math.min(Math.max(this.projectionRefreshFailures - 1, 0), 5))
    );
    this.projectionRefreshTimer = setTimeout(() => {
      this.projectionRefreshTimer = null;
      if (this.projectionRefreshPending) void this.refreshStationProjection(false);
    }, retryMs);
  }

  async refreshStationProjection(resetFailureBackoff = true) {
    return this.runProjectionRefresh(resetFailureBackoff, false);
  }

  private refreshStationProjectionAfterHealthCheck() {
    return this.runProjectionRefresh(false, true);
  }

  private async runProjectionRefresh(resetFailureBackoff: boolean, allowExhaustedProbe: boolean) {
    if (this.disposed) return;
    if (resetFailureBackoff) {
      this.projectionRefreshFailures = 0;
      if (this.projectionRefreshTimer !== null) clearTimeout(this.projectionRefreshTimer);
      this.projectionRefreshTimer = null;
    }
    if (!resetFailureBackoff && this.projectionRefreshFailures >= PROJECTION_REFRESH_GIVE_UP_AFTER) {
      if (!allowExhaustedProbe) return;
      // A healthy API snapshot is the circuit breaker's half-open signal. Do
      // not queue that probe behind an authoritative operation or another
      // projection request: the next health tick can try again, while this
      // keeps the exhausted state to at most one backend read per health poll.
      if (this.projectionRefreshInFlight || this.projectionRefreshBlocked()) return;
    }
    // A setting can be saved while a scan, command, or external operation is
    // active. Coalesce those requests and retry once the authoritative work
    // settles instead of dropping the projection update permanently.
    this.projectionRefreshPending = true;
    if (this.projectionRefreshInFlight || this.projectionRefreshBlocked()) {
      this.scheduleProjectionRefresh();
      return;
    }
    if (this.projectionRefreshTimer !== null) clearTimeout(this.projectionRefreshTimer);
    this.projectionRefreshTimer = null;
    this.projectionRefreshPending = false;
    this.projectionRefreshInFlight = true;
    const revision = this.listRevisions.next();
    const capturedStationRevisions = this.gates.snapshotStationRevisions();
    try {
      const updated = await GetCurrentStationInfo();
      this.projectionRefreshFailures = 0;
      if (this.projectionRefreshBlocked()) {
        this.projectionRefreshPending = true;
        return;
      }
      if (!this.applyStationList(updated, revision, capturedStationRevisions) && !this.disposed) {
        // Another authoritative result invalidated this read while it was in
        // flight. One final cached read makes the saved projection setting
        // observable even when that operation did not return station data.
        this.projectionRefreshPending = true;
      }
    } catch (error) {
      // Projection refreshes follow a successfully saved setting and should
      // not replace the current operation status with a transient read error.
      console.error('Station projection refresh failed:', error);
      if (!this.disposed) {
        this.projectionRefreshFailures = Math.min(this.projectionRefreshFailures + 1, PROJECTION_REFRESH_GIVE_UP_AFTER);
        if (this.projectionRefreshFailures >= PROJECTION_REFRESH_GIVE_UP_AFTER) {
          this.projectionRefreshPending = false;
        } else {
          this.projectionRefreshPending = true;
        }
      }
    } finally {
      this.projectionRefreshInFlight = false;
      if (this.projectionRefreshPending) this.scheduleProjectionRefresh();
    }
  }

  private reconcileExternalOperations(operations: Array<{ id: number; kind?: string }>, revision: number) {
    if (revision <= 0 && this.externalOperationRevision > 0) return;
    if (revision > 0 && revision < this.externalOperationRevision) return;
    if (revision > 0) this.externalOperationRevision = revision;
    const nextIds = new Set(
      operations.map((operation) => Number(operation.id)).filter((id) => Number.isFinite(id))
    );
    if ([...nextIds].some((id) => !this.externalOperationIds.has(id))) {
      this.invalidateForExternalOperation();
    }
    this.externalOperationIds = nextIds;
    this.externalOperationRunning = this.externalOperationIds.size > 0;
    // The auto-sleep busy flag is normally driven by the auto-sleep event
    // stream, but a lost terminal event would otherwise lock the UI forever.
    // Reconcile it against the authoritative operation snapshot: the backend
    // tracks the running auto-sleep action as a kind="auto-sleep" operation,
    // so its absence means the action settled and the flag must clear. Only
    // ever clear here; the event stream still owns arming the flag. A snapshot
    // that still lists the action resets the idle count so a single stale
    // snapshot (issued before the action started) cannot clear the flag.
    if (operations.some((operation) => operation.kind === 'auto-sleep')) {
      this.autoSleepEvents.noteActiveSnapshot();
    } else {
      this.autoSleepEvents.reconcileIdle();
    }
    // A deferred startup scan can run once this snapshot shows the locks clear.
    this.maybeRunDeferredStartupScan();
  }

  private invalidateForExternalOperation() {
    // HTTP work can start while a status/list read is awaiting the backend.
    // Its eventual station-update event owns the next authoritative snapshot,
    // so prevent the older read and status message from committing meanwhile.
    // Do not invalidate a local scan or command: an HTTP request announced
    // during an exclusive operation will normally be rejected as busy, and
    // must not discard the already accepted local result.
    const statusRefreshInFlight = this.globalOperation === 'status-refresh';
    const projectionOwnsCurrentRevision = this.projectionRefreshInFlight &&
      this.globalOperation === 'idle' && !this.anyDeviceOperation &&
      !this.externalScanning && !this.autoSleepRunning && !this.externalOperationRunning;
    if (!statusRefreshInFlight && !projectionOwnsCurrentRevision) return;
    if (statusRefreshInFlight) this.gates.beginStatusOperation();
    this.listRevisions.next();
    if (this.projectionRefreshInFlight) {
      this.projectionRefreshPending = true;
      this.scheduleProjectionRefresh();
    }
  }

  setStatusPollIntervalSeconds(intervalSeconds: number) {
    if (this.disposed || !Number.isFinite(intervalSeconds)) return;
    // An explicit settings save must supersede the startup getter even when
    // the value already matches the runtime default and applying it is a no-op.
    this.statusPollIntervalPreferenceRevision += 1;
    this.applyStatusPollIntervalSeconds(intervalSeconds);
  }

  setScanOnStartupEnabled(enabled: boolean) {
    if (this.disposed) return;
    this.scanOnStartupPreferenceRevision += 1;
    this.scanOnStartupEnabled = enabled;
    // A deferred scan belongs to the startup preference that requested it.
    // Turning that preference off must also cancel work queued behind a
    // startup lock; enabling it again applies to the next application start.
    if (!enabled) this.setStartupScanDeferred(false);
  }

  refreshAPIStatus() {
    return this.apiStatus.refresh();
  }

  private applyStatusPollIntervalSeconds(intervalSeconds: number) {
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

  // Delivers the immediate refresh promised by a polling re-enable that landed
  // while the startup barrier was still gating status checks. A no-op unless
  // such a re-enable actually happened.
  private runOwedImmediateStatusCheck() {
    if (!this.owedImmediateStatusCheck) return;
    this.owedImmediateStatusCheck = false;
    if (this.disposed || !this.statusPollingEnabled) return;
    void this.periodicStatusCheck();
  }

  setStatusPollingEnabled(enabled: boolean) {
    if (this.disposed) return;
    // Count the user's intent before the equality fast path. At startup the
    // in-memory default can already equal a newly saved value while an older
    // backend read is still in flight.
    this.statusPollingEnabledPreferenceRevision += 1;
    this.applyStatusPollingEnabled(enabled);
  }

  private applyStatusPollingEnabled(enabled: boolean) {
    if (this.disposed || this.statusPollingEnabled === enabled) return;
    this.statusPollingEnabled = enabled;
    if (this.statusCheckInterval) clearInterval(this.statusCheckInterval);
    this.statusCheckInterval = null;
    if (enabled) {
      const intervalMs = this.statusPollIntervalSeconds * 1000;
      this.statusCheckInterval = setInterval(() => {
        void this.periodicStatusCheck();
      }, intervalMs);
      // Re-enabling polling is an explicit request for live station state, so
      // do not make the user wait for the next interval tick. While the
      // startup barrier still gates status checks the call is a no-op; owe the
      // refresh so it runs as soon as the barrier clears instead of waiting a
      // full poll interval.
      this.owedImmediateStatusCheck = this.startupPending;
      void this.periodicStatusCheck();
    } else {
      // Recompute cached age-based fields immediately, then API-health polls
      // keep them current without reading the Bluetooth devices.
      void this.refreshStationProjection();
    }
  }

  withStationChanges(current: StationInfo, changes: Partial<StationInfo>): StationInfo {
    return this.fleet.patch(current, changes);
  }

  mount() {
    // A disposed store stays inert: its gates and pollers were permanently
    // torn down, so re-mounting could only register listeners whose
    // callbacks no-op on the disposed flag. Refuse instead of silently
    // leaking dead subscriptions.
    if (this.mounted || this.disposed) return;
    this.mounted = true;
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
      // Validate the phase before consuming the revision: an unapplied event
      // must not advance the gate, or a later replay of the same revision
      // could never be applied.
      if (event.phase !== 'started' && event.phase !== 'finished') return;
      if (revision > 0 && revision <= this.externalOperationRevision) return;
      if (revision > 0) this.externalOperationRevision = revision;
      if (event.phase === 'started') {
        const newlyStarted = !this.externalOperationIds.has(id);
        this.externalOperationIds.add(id);
        if (newlyStarted) this.invalidateForExternalOperation();
      } else this.externalOperationIds.delete(id);
      this.externalOperationRunning = this.externalOperationIds.size > 0;
      if (!this.externalOperationRunning) this.maybeRunDeferredStartupScan();
    });
    // Register operation listeners before the first asynchronous health poll
    // so a newly started HTTP action cannot slip through the mount window.
    this.applyStatusPollIntervalSeconds(DEFAULT_STATUS_POLL_INTERVAL_SECONDS);
    const startupStatusPollIntervalRevision = this.statusPollIntervalPreferenceRevision;
    const statusPollIntervalReady = GetStatusPollIntervalSeconds()
      .then((intervalSeconds) => {
        if (this.statusPollIntervalPreferenceRevision === startupStatusPollIntervalRevision) {
          this.applyStatusPollIntervalSeconds(intervalSeconds);
        }
      })
      .catch(() => {
        // Keep the default interval when the persisted preference cannot be read.
      });
    const startupStatusPollingEnabledRevision = this.statusPollingEnabledPreferenceRevision;
    const statusPollingPreferenceReady = GetStatusPollingEnabled()
      .then((enabled) => {
        if (this.statusPollingEnabledPreferenceRevision === startupStatusPollingEnabledRevision) {
          this.applyStatusPollingEnabled(enabled);
        }
      })
      .catch(() => {
        // Keep automatic station refresh enabled when the preference is unreadable.
      });
    const startupScanPreferenceRevision = this.scanOnStartupPreferenceRevision;
    // Set once the mount decision below has run, so a scan-on-startup
    // preference that resolves after the decision (a slow getter or a barrier
    // timeout) can still arm the deferred startup scan instead of being
    // silently lost for the session.
    let startupDecisionApplied = false;
    const startupScanPreferenceReady = GetScanOnStartup()
      .then((enabled) => {
        if (!this.disposed && this.scanOnStartupPreferenceRevision === startupScanPreferenceRevision) {
          this.scanOnStartupEnabled = enabled;
          if (enabled && startupDecisionApplied &&
            startupScanEpoch === this.gates.currentScanEpoch) {
            this.setStartupScanDeferred(true);
          }
        }
      })
      .catch(() => {
        // Fail closed only when no newer saved preference has superseded this
        // startup read. A delayed failure must not erase the user's choice.
        if (!this.disposed && this.scanOnStartupPreferenceRevision === startupScanPreferenceRevision) {
          this.scanOnStartupEnabled = false;
        }
      });
    // A non-default persisted interval restarts ApiStatusPoller and revision-
    // gates its first request. Read the promise only after that restart has
    // happened so startup waits for the response that can actually commit.
    const startupAPIStatusReady = statusPollIntervalReady.then(() => this.startupAPIStatusReady);
    void (async () => {
      // A rejected probe belongs in the "no authoritative observation" bucket
      // (null), matching the timeout fallback below: classifying it as "not
      // scanning" would start the configured startup scan against a backend
      // whose state is unknown, and a busy rejection would then drop the
      // scan for the session instead of deferring it.
      const startupScanningProbe: Promise<boolean | null> = IsScanning().catch(() => null);
      const startupBarrier = Promise.all([
        startupScanningProbe,
        startupScanPreferenceReady,
        // Wait for the operation-health snapshot started above. It recovers
        // automatic sleep (and HTTP work) that began before event listeners
        // mounted, closing the only window where an internal scan could be
        // misidentified as a user-stoppable external scan.
        startupAPIStatusReady,
        statusPollingPreferenceReady
      ]);
      // A hung backend getter must not hold startupPending forever. The null
      // fallback marks "no authoritative observation" (barrier timeout or a
      // rejected probe): scan ownership then stays undecidable until the first
      // health snapshot lands, so neither adoption nor a direct startup scan
      // may run — both could misclassify or be rejected by an internal scan.
      const startupScanning = await resolveWithTimeout(
        startupBarrier.then(() => startupScanningProbe),
        STARTUP_PROBE_TIMEOUT_MS,
        null
      );
      // Do not allow the first polling tick to acquire the backend operation
      // lock before the initial external-scan check can start the local scan.
      this.startupPending = false;
      startupDecisionApplied = true;
      // An external scan event may have arrived while this initial query was
      // pending. Do not let its older result overwrite the newer event state.
      if (this.disposed || startupScanEpoch !== this.gates.currentScanEpoch) {
        // The startup scan decision belongs to the newer scan owner now, but a
        // polling re-enable during the barrier still owes an immediate
        // refresh. This early return is the only exit that skips the
        // fulfillment below, so deliver it here; periodicStatusCheck
        // self-gates against whichever work the newer owner is running.
        this.runOwedImmediateStatusCheck();
        return;
      }
      if (startupScanning === null) {
        if (this.scanOnStartupEnabled) this.setStartupScanDeferred(true);
      } else if (startupScanning) {
        // An auto-sleep scan is internal; adopting it would expose Stop for
        // it. Skip adoption and let the periodic check take over afterwards.
        if (!this.autoSleepRunning && !this.externalOperationRunning) {
          await this.externalScan.adoptUnknown();
        }
      } else if (this.scanOnStartupEnabled) {
        if (!await this.startScan() && this.scanOnStartupEnabled) {
          // Locked at startup (an auto-sleep or HTTP operation was already
          // running). Defer and retry once the lock releases. Re-check the
          // preference after await so a concurrent settings save cannot
          // recreate a deferred scan that it just cancelled.
          this.setStartupScanDeferred(true);
        }
      } else {
        void this.recoverPreMountScanOutcome();
      }
      // Fulfill the owed immediate status refresh only after the startup scan
      // decision has been attempted: it flips globalOperation to
      // 'status-refresh' synchronously, which would otherwise trip the
      // scanLocked guard and push the configured startup scan into the slower
      // deferred-retry path.
      this.runOwedImmediateStatusCheck();
    })();
  }

  // A scan that started and finished between backend startup and listener
  // mounting never delivered any events. Without this probe its terminal
  // outcome (and its station list) stays invisible until the first periodic
  // poll, which a user-tuned interval can delay for minutes.
  private async recoverPreMountScanOutcome() {
    const status = await GetScanStatus().catch(() => null);
    if (this.disposed || !status || !isTerminalScanState(status.state)) return;
    if (this.globalOperation !== 'idle' || this.externalScanning || this.isLoading) return;
    await this.externalScan.recoverUntracked();
  }

  // Retries a startup scan that was blocked by a lock once that lock clears.
  // Called from every site that can release the auto-sleep / external-op busy
  // state; the pending flag keeps it a no-op everywhere else.
  private maybeRunDeferredStartupScan() {
    if (this.disposed || !this.startupScanDeferred) return;
    if (!this.scanOnStartupEnabled) {
      this.setStartupScanDeferred(false);
      return;
    }
    // Before the first health snapshot lands, lock state cannot distinguish
    // an internal scan from an idle backend; starting the deferred scan would
    // fail busy against an auto-sleep scan and lose the deferral.
    if (this.scanLocked || this.isLoading || !this.startupScanOwnershipKnown) {
      this.armDeferredStartupScanRetry();
      return;
    }
    this.setStartupScanDeferred(false);
    void this.startScan().then((started) => {
      // A lock can land between the guard above and the scan start, or the
      // backend can reject a started attempt as busy; re-arm so the
      // configured startup scan is not silently dropped. Retry through the
      // interval instead of an immediate attempt: a busy backend is still
      // holding its lock, and looping would hammer it without delay.
      if (!started && !this.disposed && this.scanOnStartupEnabled) {
        this.startupScanDeferred = true;
        this.armDeferredStartupScanRetry();
      }
    }).catch(() => {
      // A rejected scan promise must not drop the configured startup scan:
      // re-arm the deferral the same way a busy rejection does.
      if (!this.disposed && this.scanOnStartupEnabled) {
        this.startupScanDeferred = true;
        this.armDeferredStartupScanRetry();
      }
    });
  }

  private setStartupScanDeferred(deferred: boolean) {
    this.startupScanDeferred = deferred;
    if (deferred) {
      // Attempt immediately: the blocking lock may already be released (for
      // example when the health snapshot landed and only the startup barrier
      // was slow). A still-held lock re-arms the retry interval inside.
      this.maybeRunDeferredStartupScan();
    } else {
      this.disarmDeferredStartupScanRetry();
    }
  }

  private armDeferredStartupScanRetry() {
    if (this.disposed || this.deferredStartupScanTimer !== null) return;
    this.deferredStartupScanTimer = setInterval(() => {
      this.maybeRunDeferredStartupScan();
    }, DEFERRED_STARTUP_SCAN_RETRY_MS);
  }

  private disarmDeferredStartupScanRetry() {
    if (this.deferredStartupScanTimer !== null) clearInterval(this.deferredStartupScanTimer);
    this.deferredStartupScanTimer = null;
  }

  dispose() {
    this.disposed = true;
    this.mounted = false;
    this.gates.dispose();
    this.listRevisions.dispose();
    this.apiStatus.dispose();
    this.scanTimer.dispose();
    this.scans.dispose();
    this.actions.dispose();
    this.fleet.stopChannelMemoryExpiry();
    this.disarmDeferredStartupScanRetry();
    this.startupScanDeferred = false;
    if (this.statusCheckInterval) clearInterval(this.statusCheckInterval);
    if (this.projectionRefreshTimer !== null) clearTimeout(this.projectionRefreshTimer);
    this.projectionRefreshTimer = null;
    this.projectionRefreshPending = false;
    this.projectionRefreshFailures = 0;
    this.owedImmediateStatusCheck = false;
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
    // A scan the user starts manually fulfills the configured scan-on-startup
    // intent; drop any deferred startup scan so the retry timer does not fire
    // a redundant automatic scan moments after the user's own scan.
    if (this.startupScanDeferred) this.setStartupScanDeferred(false);
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

  canStartBulkPower(state: PowerTarget) {
    return this.actions.canStartBulkPower(state);
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
