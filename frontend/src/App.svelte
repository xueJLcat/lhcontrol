<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { flip } from 'svelte/animate';
  import { cubicOut } from 'svelte/easing';
  import { fade } from 'svelte/transition';
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
  } from './lib/backend';
  import { EventsOn } from '../wailsjs/runtime/runtime';
  import { station as stationModels } from '../wailsjs/go/models';
  import { Activity, CircleAlert, Radar } from 'lucide-svelte';
  import type { PowerFeedback, PowerTarget, StationInfo } from './lib/types';
  import { classifyScanError, type ScanErrorInfo } from './lib/scan-error';
  import {
    canSetPower, channelChangeBlockedReason, hasCurrentChannel, hasVerifiedPowerState, isCurrentPowerState,
    powerTargetLabel, sameStationInfo, stateLabel
  } from './lib/station';
  import { formatBulkResult, formatTerminalScanResult, summarizeBulkResult } from './lib/result-format';
  import { clearToasts, pushToast } from './lib/toast';
  import { dur } from './lib/motion';
  import { deriveOperationLocks, type GlobalOperation } from './lib/operation-state';
  import { ExternalScanCoordinator, type ExternalScanEvent } from './lib/external-scan';
  import { OperationGate } from './lib/operation-gate';
  import { PowerFeedbackRegistry } from './lib/power-feedback';
  import { RevisionGate } from './lib/revision-gate';
  import { ScanTimer } from './lib/scan-timer';
  import { ApiStatusPoller } from './lib/api-status-poller';
  import { ChannelMemory } from './lib/channel-memory';
  import AppHeader from './lib/components/AppHeader.svelte';
  import StationCard from './lib/components/StationCard.svelte';
  import ChannelMap from './lib/components/ChannelMap.svelte';
  import DetailsDrawer from './lib/components/DetailsDrawer.svelte';
  import ChannelModal from './lib/components/ChannelModal.svelte';
  import StatusFooter from './lib/components/StatusFooter.svelte';
  import Toast from './lib/components/Toast.svelte';
  import ScanRecovery from './lib/components/ScanRecovery.svelte';
  import BulkConfirmModal from './lib/components/BulkConfirmModal.svelte';

  let stations = $state<StationInfo[]>([]);
  let statusMessage = $state('Ready to scan.');
  let gattOperations = $state(new Set<string>());
  let configOperations = $state(new Set<string>());
  let powerTargetByAddress = $state<Record<string, PowerTarget | undefined>>({});
  let powerFeedbackMap = $state<Record<string, PowerFeedback | undefined>>({});
  const powerFeedback = new PowerFeedbackRegistry((next) => powerFeedbackMap = next);
  let globalOperation = $state<GlobalOperation>('idle');
  let bulkTarget = $state<PowerTarget | null>(null);
  let editingAddress = $state<string | null>(null);
  let selectedAddress = $state<string | null>(null);
  let channelEditorOpen = $state(false);
  let channelError = $state('');
  let channelWarning = $state(false);
  let scanError = $state<ScanErrorInfo | null>(null);
  let bulkConfirmTarget = $state<PowerTarget | null>(null);
  let statusCheckInterval: ReturnType<typeof setInterval> | null = null;
  let cancelExternalScanListener: (() => void) | null = null;
  let cancelExternalScanFailureListener: (() => void) | null = null;
  let cancelExternalScanStartedListener: (() => void) | null = null;
  let cancelExternalScanCancelledListener: (() => void) | null = null;
  let apiRunning = $state(false);
  let apiAddress = $state('');
  let apiError = $state('');
  let configWarnings = $state<string[]>([]);
  let configWritable = $state(true);
  let externalScanning = $state(false);
  let stoppingScan = $state(false);
  let stopRequestPending = false;
  let stopRequestGeneration = 0;
  let scanElapsed = $state(0);
  const scanTimer = new ScanTimer((seconds) => scanElapsed = seconds);
  const listRevisions = new RevisionGate();
  const gates = new OperationGate();
  let disposed = false;
  let startupPending = true;

  // Coordination for scans started outside this UI (the HTTP API). All host
  // callbacks write through component state so Svelte reactivity keeps the
  // template in sync.
  const externalScan = new ExternalScanCoordinator({
    isDisposed: () => disposed,
    localScanRunning: () => globalOperation === 'scanning',
    externalScanning: () => externalScanning,
    setExternalScanning: (value) => externalScanning = value,
    scanEpoch: () => gates.currentScanEpoch,
    statusEpoch: () => gates.currentStatusEpoch,
    beginScanEpoch: () => gates.beginScanEpoch(),
    beginStatusOperation: () => gates.beginStatusOperation(),
    canCommitOperation: (epoch) => gates.canCommitOperation(epoch),
    canCommitStatus: (epoch) => gates.canCommitStatus(epoch),
    nextListRevision: () => listRevisions.next(),
    isListRevisionCurrent: (revision) => listRevisions.isCurrent(revision),
    snapshotStationRevisions: () => gates.snapshotStationRevisions(),
    prepareForScan: (clearOperations) => prepareForScan(clearOperations),
    applyStationList: (updated, revision, captured) => applyStationList(updated, revision, captured),
    seenInLatestScanCount: () => stations.filter((station) => station.seenInLatestScan).length,
    knownStationCount: () => stations.length,
    setStatusMessage: (message) => statusMessage = message,
    setStoppingScan: (value) => stoppingScan = value,
    beginScanTimer: () => beginScanTimer(),
    maybeEndScanTimer: () => maybeEndScanTimer(),
    isScanning: () => IsScanning(),
    getScanStatus: () => GetScanStatus(),
    getCurrentStationInfo: () => GetCurrentStationInfo(),
    notifyExternalScanFailure: (message) => pushToast(message)
  });

  // HTTP API health polling and config-warning de-duplication live in a
  // dedicated poller; the callbacks write through component state so Svelte
  // reactivity keeps the template in sync.
  const apiStatus = new ApiStatusPoller({
    isDisposed: () => disposed,
    commitStatus: (status) => {
      apiRunning = status.running;
      apiError = status.error;
      apiAddress = status.address;
      configWarnings = status.warnings ?? [];
      configWritable = status.configWritable ?? true;
    },
    commitFailure: (error) => {
      apiRunning = false;
      apiError = error;
    },
    reportConfigWarning: (warning) => pushToast(warning, 'warning')
  });

  // Display-only channel memory (see ChannelMemory): keeps the channel bar,
  // card chip and card ordering stable across transient backend wipes.
  const channelMemory = new ChannelMemory();

  function displayChannel(station: StationInfo): number {
    return channelMemory.displayChannel(station);
  }

  // Keep the memory current before any derived list recomputes. The pre
  // effect runs before the render flush so the cache is fresh when sort keys
  // and child props are evaluated.
  $effect.pre(() => {
    channelMemory.refresh(stations);
  });

  const sortedStations = $derived([...stations].sort((a, b) => {
    const ac = displayChannel(a) || Number.MAX_SAFE_INTEGER;
    const bc = displayChannel(b) || Number.MAX_SAFE_INTEGER;
    return ac - bc || a.name.localeCompare(b.name) || a.address.localeCompare(b.address);
  }));
  const selectedStation = $derived(stations.find((station) => station.address === selectedAddress) ?? null);
  // A station can drop out of the list while its drawer is open (backend
  // list replacement). Clear the stale selection so the drawer does not
  // silently reopen if the same address reappears later, and drop the
  // channel-editor state with it: leaving channelEditorOpen true would make
  // the next selected station open an inert drawer plus a channel modal that
  // belongs to the previous selection. Only the station list drives this
  // guard; the addresses are read untracked to avoid a reactive cycle with
  // selectedStation.
  $effect(() => {
    const list = stations;
    const address = untrack(() => selectedAddress);
    if (address !== null && !list.some((station) => station.address === address)) {
      selectedAddress = null;
      channelEditorOpen = false;
      channelError = '';
      channelWarning = false;
    }
    const renaming = untrack(() => editingAddress);
    if (renaming !== null && !list.some((station) => station.address === renaming)) {
      editingAddress = null;
    }
  });
  const conflictStations = $derived(stations.filter((station) => station.channelConflict));
  const conflictDetails = $derived((() => {
    const byChannel = new Map<number, string[]>();
    for (const station of conflictStations) {
      const key = station.channel > 0 ? station.channel : -1;
      byChannel.set(key, [...(byChannel.get(key) ?? []), station.name]);
    }
    return [...byChannel.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([channel, names]) => channel > 0 ? `CH ${channel}: ${names.join(' + ')}` : names.join(' + '))
      .join(' · ');
  })());
  const fleetOn = $derived(stations.filter((station) => hasVerifiedPowerState(station, 'on')).length);
  const fleetStandby = $derived(stations.filter((station) => hasVerifiedPowerState(station, 'standby')).length);
  const fleetSleep = $derived(stations.filter((station) => hasVerifiedPowerState(station, 'sleep')).length);
  // Stations that exist but have no verified state (stale, unconfirmed or
  // booting) still count, so the summary never goes quiet while cards exist.
  const fleetUnverified = $derived(Math.max(0, stations.length - fleetOn - fleetStandby - fleetSleep));
  const actionableOn = $derived(stations.filter((station) => canSetPower(station, 'on')));
  const actionableStandby = $derived(stations.filter((station) => canSetPower(station, 'standby')));
  const actionableSleep = $derived(stations.filter((station) => canSetPower(station, 'sleep')));
  const occupiedChannels = $derived((() => {
    const occupied = new Map<number, string[]>();
    const candidates = stations
      .filter((station) => hasCurrentChannel(station) && station.address !== selectedAddress)
      .sort((a, b) => a.name.localeCompare(b.name) || a.address.localeCompare(b.address));
    for (const station of candidates) {
      occupied.set(station.channel, [...(occupied.get(station.channel) ?? []), station.name]);
    }
    return occupied;
  })());
  const hasUnknownVisibleChannel = $derived(stations.some(
    (station) => station.isPresent && station.address !== selectedAddress &&
      (station.presenceUncertain || !station.scanFresh || !station.channelFresh || station.channel === 0)
  ));
  const operationLocks = $derived(deriveOperationLocks({
    global: globalOperation,
    externalScanning,
    gattAddresses: gattOperations,
    configAddresses: configOperations
  }));
  const isLoading = $derived(globalOperation === 'scanning');
  const isBulkLoading = $derived(globalOperation === 'bulk-power');
  const isStatusChecking = $derived(globalOperation === 'status-refresh');
  const scanningActive = $derived(isLoading || externalScanning || stoppingScan);
  const anyDeviceOperation = $derived(operationLocks.anyDeviceOperation);
  const scanLocked = $derived(operationLocks.scanLocked);
  const bulkLocked = $derived(operationLocks.bulkLocked);
  const stationLocked = $derived(operationLocks.stationLocked);
  // The periodic status reader occupies one of the backend's two device
  // operation slots. Keep the remaining slot available to one foreground
  // action instead of allowing a second action that will fail with Busy.
  const foregroundGattCapacity = $derived(isStatusChecking ? 1 : 2);
  const gattCapacityReached = $derived(gattOperations.size >= foregroundGattCapacity);

  // Fleet trust partition behind the bulk scope row and the confirmation
  // gate: a station counts as verified only while its presence, scan
  // freshness and confirmed power state are all current. Everything else is
  // included in bulk commands at the backend's discretion, which the user
  // must see before confirming.
  const visibleCount = $derived(stations.filter((station) => station.isPresent && !station.presenceUncertain).length);
  const invisibleCount = $derived(stations.filter((station) => !station.isPresent).length);
  const uncertainCount = $derived(stations.filter((station) => station.isPresent && station.presenceUncertain).length);
  const untrustedCount = $derived(stations.filter((station) =>
    !station.isPresent || station.presenceUncertain || !station.scanFresh ||
    !station.powerStateConfirmed || station.powerState < 0 || station.powerState === 3
  ).length);

  function stationBusy(address: string): boolean {
    return gattOperations.has(address) || configOperations.has(address);
  }

  function beginScanTimer() {
    scanTimer.begin();
  }

  function maybeEndScanTimer() {
    // A local scan and an external scan can overlap; the timer belongs to
    // whichever is still running, so only stop when both have finished.
    if (globalOperation !== 'scanning' && !externalScanning) scanTimer.end();
  }

  function setGattBusy(address: string, busy: boolean) {
    const next = new Set(gattOperations);
    if (busy) next.add(address);
    else next.delete(address);
    gattOperations = next;
  }

  function setConfigBusy(address: string, busy: boolean) {
    const next = new Set(configOperations);
    if (busy) next.add(address);
    else next.delete(address);
    configOperations = next;
  }

  function commitStations(updated: StationInfo[]) {
    // Reuse the previous object for unchanged stations so no-op background
    // refreshes do not re-render cards or retrigger CSS transitions.
    const previousByAddress = new Map(stations.map((station) => [station.address, station]));
    stations = updated.map((station) => {
      const previous = previousByAddress.get(station.address);
      return previous && sameStationInfo(previous, station) ? previous : station;
    });
    powerFeedback.reconcile(stations);
  }

  function gattLockedFor(address: string): boolean {
    return stationLocked || (gattCapacityReached && !gattOperations.has(address));
  }

  // Template-side lock lookup. Template expressions only invalidate on the
  // variables they reference syntactically, so the reactive inputs are passed
  // explicitly instead of being read inside a helper call.
  function computeGattLocked(
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
  const gattLockedByAddress = $derived(computeGattLocked(stations, stationLocked, gattCapacityReached, gattOperations));

  // Same explicit-dependency pattern: the template reads per-address busy
  // flags from this derived map instead of calling stationBusy(), whose
  // reactive inputs a call expression would hide from invalidation.
  function computeBusy(
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
  const busyByAddress = $derived(computeBusy(stations, gattOperations, configOperations));

  function applyStationList(
    updated: StationInfo[] | null | undefined,
    revision: number,
    capturedStationRevisions?: Map<string, number>
  ): boolean {
    if (disposed || !listRevisions.isCurrent(revision)) return false;
    const incoming = updated || [];
    if (!capturedStationRevisions) {
      commitStations(incoming);
      return true;
    }
    const currentByAddress = new Map(stations.map((station) => [station.address, station]));
    const incomingAddresses = new Set(incoming.map((station) => station.address));
    const merged = incoming.map((station) =>
      gates.stationRevision(station.address) === (capturedStationRevisions.get(station.address) ?? 0)
        ? station
        : currentByAddress.get(station.address) ?? station
    );
    for (const current of stations) {
      if (!incomingAddresses.has(current.address) &&
        gates.stationRevision(current.address) !== (capturedStationRevisions.get(current.address) ?? 0)) {
        merged.push(current);
      }
    }
    commitStations(merged);
    return true;
  }

  function prepareForScan(clearOperations = true) {
    cancelRename();
    channelEditorOpen = false;
    channelError = '';
    channelWarning = false;
    scanError = null;
    bulkConfirmTarget = null;
    // A locally running scan has acquired the backend's exclusive operation
    // lock. An accepted external scan may still be waiting for existing work,
    // so preserve those visible operations until their promises settle.
    if (clearOperations) {
      gattOperations = new Set();
      configOperations = new Set();
      gates.clearOperationTokens();
    }
    powerTargetByAddress = {};
    powerFeedback.clearAll();
  }

  function mergeStationUpdates(updated: StationInfo[]) {
    if (!updated.length) return;
    const byAddress = new Map(updated.map((station) => [station.address, station]));
    const existingAddresses = new Set(stations.map((station) => station.address));
    commitStations([
      ...stations.map((station) => byAddress.get(station.address) ?? station),
      ...updated.filter((station) => !existingAddresses.has(station.address))
    ]);
  }

  function withStationChanges(current: StationInfo, changes: Partial<StationInfo>): StationInfo {
    return stationModels.StationInfo.createFrom({ ...current, ...changes });
  }

  onMount(async () => {
    const startupScanEpoch = gates.currentScanEpoch;
    apiStatus.start(15000);
    cancelExternalScanStartedListener = EventsOn('external-scan-started', (value: unknown) => {
      externalScan.handleStarted(value as ExternalScanEvent);
    });
    cancelExternalScanListener = EventsOn('external-scan-completed', (value: unknown) => {
      externalScan.handleCompleted(value as ExternalScanEvent);
    });
    cancelExternalScanFailureListener = EventsOn('external-scan-failed', (value: unknown) => {
      externalScan.handleFailed(value as ExternalScanEvent);
    });
    cancelExternalScanCancelledListener = EventsOn('external-scan-cancelled', (value: unknown) => {
      externalScan.handleCancelled(value as ExternalScanEvent);
    });
    statusCheckInterval = setInterval(periodicStatusCheck, 15000);
    const startupScanning = await IsScanning().catch(() => false);
    // Do not allow the first polling tick to acquire the backend operation
    // lock before the initial external-scan check can start the local scan.
    startupPending = false;
    // An external scan event may have arrived while this initial query was
    // pending. Do not let its older result overwrite the newer event state.
    if (disposed || startupScanEpoch !== gates.currentScanEpoch) return;
    if (startupScanning) {
      await externalScan.adoptUnknown();
    } else {
      await handleScanClick();
    }
  });

  onDestroy(() => {
    disposed = true;
    gates.dispose();
    clearToasts();
    listRevisions.dispose();
    apiStatus.dispose();
    scanTimer.dispose();
    if (statusCheckInterval) clearInterval(statusCheckInterval);
    powerFeedback.clearAll();
    cancelExternalScanListener?.();
    cancelExternalScanFailureListener?.();
    cancelExternalScanStartedListener?.();
    cancelExternalScanCancelledListener?.();
  });

  async function periodicStatusCheck() {
    if (startupPending || isStatusChecking || isLoading || isBulkLoading || anyDeviceOperation) return;
    globalOperation = 'status-refresh';
    const statusOperation = gates.currentStatusEpoch;
    const revision = listRevisions.next();
    const capturedStationRevisions = gates.snapshotStationRevisions();
    try {
      const scanning = await IsScanning();
      if (disposed || !listRevisions.isCurrent(revision)) return;
      const wasExternalScanning = externalScanning;
      if (scanning && !isLoading && !externalScanning) {
        await externalScan.adoptUnknown();
        return;
      }
      externalScanning = scanning && !isLoading;
      if (!scanning) {
        stoppingScan = false;
        if (wasExternalScanning) {
          externalScan.markRecoveryPending();
        }
        if (externalScan.hasPendingRecovery()) {
          await externalScan.recoverTrackedTerminal(revision, statusOperation, capturedStationRevisions);
        } else if (externalScan.hasPendingTerminal()) {
          // A scan ended while no listener tracked it. Apply its terminal
          // outcome now instead of waiting for the next external scan event.
          await externalScan.recoverUntracked();
          return;
        } else if (!applyStationList(await CheckAllStationStatuses(), revision, capturedStationRevisions)) return;
        maybeEndScanTimer();
      } else {
        beginScanTimer();
        if (externalScanning) {
          const scanStatus = await GetScanStatus().catch(() => null);
          // A pending stop owns the status message; letting the poll
          // overwrite "Stopping scan..." contradicts the header button.
          if (!disposed && listRevisions.isCurrent(revision) && scanStatus &&
            !stoppingScan && !stopRequestPending) {
            statusMessage = scanStatus.state === 'starting'
              ? 'Preparing external scan...'
              : 'External scan in progress...';
          }
        }
      }
    } catch (error) {
      if (disposed || !listRevisions.isCurrent(revision)) return;
      console.error('Periodic status check failed:', error);
      const fallback = await GetCurrentStationInfo().catch(() => stations);
      if (!applyStationList(fallback, revision, capturedStationRevisions)) return;
      if (gates.canCommitStatus(statusOperation)) statusMessage = `Status refresh incomplete: ${error}`;
    } finally {
      if (!disposed && globalOperation === 'status-refresh') globalOperation = 'idle';
    }
  }

  async function handleScanClick() {
    if (isLoading || scanLocked) return;
    prepareForScan();
    // A pending stop belongs to the superseded scan. Its StopScan promise
    // can still be settling while the empty fleet lets the user start a new
    // scan; the new scan must show its own running state and a working Stop.
    // The old stop's finally never clears stoppingScan while a scan runs,
    // so this reset cannot be clobbered.
    stoppingScan = false;
    externalScan.resetForLocalScan();
    globalOperation = 'scanning';
    const statusOperation = gates.beginStatusOperation();
    beginScanTimer();
    const operationEpoch = gates.beginScanEpoch();
    const revision = listRevisions.next();
    statusMessage = 'Scanning for base stations...';
    try {
      if (!applyStationList(await ScanAndFetchStations(), revision)) return;
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!gates.canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision) || !gates.canCommitStatus(statusOperation)) return;
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = formatTerminalScanResult({
        state: scanStatus?.state ?? 'completed',
        found, known: stations.length,
        error: scanStatus?.error,
        warnings: scanStatus?.warnings
      });
      scanError = null;
    } catch (error) {
      if (!gates.canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision) || !gates.canCommitStatus(statusOperation)) return;
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!gates.canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (updated) applyStationList(updated, revision);
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!gates.canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision) || !gates.canCommitStatus(statusOperation)) return;
      if (stoppingScan || scanStatus?.state === 'cancelled') {
        if (!stopRequestPending) stoppingScan = false;
        statusMessage = 'Scan stopped.';
        scanError = null;
      } else {
        statusMessage = `Scan failed: ${error}`;
        pushToast(`Scan failed: ${error}`);
        // The footer/toast are transient; the main area keeps a persistent,
        // actionable recovery card until the next scan attempt.
        scanError = classifyScanError(error);
      }
    } finally {
      if (!disposed && globalOperation === 'scanning') globalOperation = 'idle';
      if (!disposed && !stopRequestPending && !externalScanning) stoppingScan = false;
      maybeEndScanTimer();
    }
  }

  async function handleStopScan() {
    if (!scanningActive || stoppingScan) return;
    const operationEpoch = gates.currentScanEpoch;
    const requestGeneration = ++stopRequestGeneration;
    stoppingScan = true;
    stopRequestPending = true;
    statusMessage = 'Stopping scan...';
    try {
      await StopScan();
      if (!gates.canCommitOperation(operationEpoch)) return;
      stopRequestPending = false;
      if (!stoppingScan) return;
      if (externalScanning) {
        await externalScan.finishStop(operationEpoch, () => stopRequestGeneration === requestGeneration);
      } else {
        if (globalOperation !== 'scanning') stoppingScan = false;
        statusMessage = 'Scan stopped.';
        maybeEndScanTimer();
      }
    } catch (error) {
      if (!gates.canCommitOperation(operationEpoch)) return;
      stopRequestPending = false;
      stoppingScan = false;
      statusMessage = `Unable to stop scan: ${error}`;
      pushToast(statusMessage);
    } finally {
      // Terminal scan events can advance scanEpoch before StopScan settles.
      // Clear this request by identity rather than treating it as stale.
      if (stopRequestGeneration === requestGeneration) {
        stopRequestPending = false;
        if (globalOperation !== 'scanning' && !externalScanning) stoppingScan = false;
      }
    }
  }

  async function fetchLatestList(revision = listRevisions.next()): Promise<boolean> {
    const capturedStationRevisions = gates.snapshotStationRevisions();
    try {
      return applyStationList(await GetCurrentStationInfo(), revision, capturedStationRevisions);
    } catch {
      return false;
    }
  }

  async function fetchStationUpdate(
    address: string,
    epoch: number,
    operationRevision: number
  ): Promise<StationInfo | null> {
    const updated = await GetCurrentStationInfo().catch(() => null);
    if (!gates.canCommitStationOperation(epoch, address, operationRevision)) return null;
    const station = updated?.find((item) => item.address === address) ?? null;
    if (station) {
      mergeStationUpdates([station]);
    }
    return station;
  }

  function powerReadbackLabel(station: StationInfo | null): string {
    if (!station || station.powerState < 0) return 'unavailable';
    return `${station.powerFresh ? 'actual' : 'last-known'} ${stateLabel(station)}`;
  }

  function channelReadbackLabel(station: StationInfo | null): string {
    if (!station || station.channel <= 0) return 'unavailable';
    return `${station.channelFresh ? 'actual' : 'last-known'} ${station.channel}`;
  }

  async function setPower(station: StationInfo, state: PowerTarget) {
    if (!canSetPower(station, state) || stationBusy(station.address) || gattLockedFor(station.address)) return;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = gates.currentScanEpoch;
    const statusOperation = gates.beginStatusOperation();
    const operationRevision = gates.beginStationOperationRevision(station.address);
    setGattBusy(station.address, true);
    powerTargetByAddress = { ...powerTargetByAddress, [station.address]: state };
    powerFeedback.set(station.address, {
      kind: 'pending',
      text: `Switching to ${targetLabel}…`,
      target: state
    });
    statusMessage = `Setting ${station.name} to ${targetLabel}…`;
    try {
      const result = await SetStationPower(station.address, state);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      mergeStationUpdates([result.station]);
      powerFeedback.set(station.address, result.skipped
        ? {
            kind: 'success', text: `Already ${targetLabel}`, target: state,
            readAt: result.station.lastPowerReadAt
          }
        : result.confirmed
        ? {
            kind: 'success', text: `${targetLabel} confirmed`, target: state,
            readAt: result.station.lastPowerReadAt
          }
        : {
            kind: 'warning',
            text: result.confirmationError
              ? `${targetLabel} sent · confirmation failed`
              : `${targetLabel} sent · status unavailable`,
            target: state,
            readAt: result.station.lastPowerReadAt
          });
      if (gates.canCommitStatus(statusOperation)) {
        statusMessage = result.skipped
          ? `${station.name} is already ${targetLabel}; no command was sent.`
          : result.confirmed
          ? `${station.name} is ${targetLabel}.`
          : result.confirmationError
            ? `${station.name}: command sent, but confirmation failed. ${result.confirmationError}`
            : `${station.name}: ${targetLabel} command sent; this firmware cannot confirm the state.`;
      }
    } catch (error) {
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      const errorText = String(error);
      const actual = await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      powerFeedback.set(station.address, {
        kind: 'error',
        text: `Failed · ${powerReadbackLabel(actual)}`,
        target: state,
        readAt: actual?.lastPowerReadAt
      });
      if (gates.canCommitStatus(statusOperation)) {
        statusMessage = `Power change failed for ${station.name}: ${errorText}`;
        pushToast(`Power change failed for ${station.name}: ${errorText}`);
      }
    } finally {
      if (gates.canCleanupStationOperation(station.address, operationRevision)) {
        const nextTargets = { ...powerTargetByAddress };
        delete nextTargets[station.address];
        powerTargetByAddress = nextTargets;
        setGattBusy(station.address, false);
      }
    }
  }

  function actionablePowerStations(state: PowerTarget): StationInfo[] {
    return stations.filter((station) => canSetPower(station, state));
  }

  // Derived instead of a template function call: call expressions hide
  // their reactive inputs from invalidation, and the header's "all at
  // state" flags must follow the station list.
  const allOn = $derived(stations.length > 0 && stations.every((item) => isCurrentPowerState(item, 'on')));
  const allStandby = $derived(stations.length > 0 && stations.every((item) => isCurrentPowerState(item, 'standby')));
  const allSleep = $derived(stations.length > 0 && stations.every((item) => isCurrentPowerState(item, 'sleep')));

  function handleBulkPower(state: PowerTarget) {
    // Do not duplicate backend capability/state decisions here. Cached
    // frontend data can be stale after scanning, while the backend refreshes
    // capabilities and returns a result for every known station.
    if (bulkLocked || actionablePowerStations(state).length === 0) return;
    // When part of the fleet is not fully verified, the command scope is no
    // longer obvious from the button; demand an explicit confirmation that
    // lists what will be affected.
    if (untrustedCount > 0) {
      bulkConfirmTarget = state;
      return;
    }
    void runBulkPower(state);
  }

  function cancelBulkPower() {
    bulkConfirmTarget = null;
  }

  async function confirmBulkPower() {
    const state = bulkConfirmTarget;
    bulkConfirmTarget = null;
    if (state) await runBulkPower(state);
  }

  async function runBulkPower(state: PowerTarget) {
    globalOperation = 'bulk-power';
    const statusOperation = gates.beginStatusOperation();
    bulkTarget = state;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = gates.currentScanEpoch;
    statusMessage = `Setting all available stations to ${targetLabel}…`;
    try {
      const result = await SetAllStationsPowerDetailed(state);
      if (!gates.canCommitOperation(operationEpoch)) return;
      mergeStationUpdates(result.results.map((item) => item.station).filter((item) => Boolean(item?.address)));
      await fetchLatestList();
      if (!gates.canCommitOperation(operationEpoch)) return;
      for (const item of result.results) {
        const feedback: Pick<PowerFeedback, 'kind' | 'text'> = item.skipped
          ? item.success && item.confirmed
            ? { kind: 'success', text: `Already ${targetLabel}` }
            : { kind: 'warning', text: `Skipped · ${item.reason || 'not actionable'}` }
            : item.success && item.confirmed
              ? { kind: 'success', text: `${targetLabel} confirmed` }
              : item.success && item.commandSent
              ? { kind: 'warning', text: `${targetLabel} sent · ${item.error || 'status unavailable'}` }
              : { kind: 'error', text: item.error || `Failed to set ${targetLabel}` };
        powerFeedback.set(item.address, {
          ...feedback,
          target: state,
          readAt: item.station?.lastPowerReadAt
        });
      }
      const summary = summarizeBulkResult(result.results);
      if (!gates.canCommitStatus(statusOperation)) return;
      statusMessage = formatBulkResult(targetLabel, summary);
      const toastKind = summary.failed.length ? 'error'
        : summary.unconfirmed || summary.skipped ? 'warning'
          : 'success';
      pushToast(`Bulk ${targetLabel}: ${formatBulkResult(targetLabel, summary)}`, toastKind);
    } catch (error) {
      if (!gates.canCommitOperation(operationEpoch)) return;
      await fetchLatestList();
      if (!gates.canCommitOperation(operationEpoch)) return;
      if (gates.canCommitStatus(statusOperation)) {
        statusMessage = `Bulk ${targetLabel} operation partially failed: ${error}`;
        pushToast(`Bulk ${targetLabel} operation partially failed: ${error}`);
      }
    } finally {
      if (!disposed) {
        if (globalOperation === 'bulk-power') globalOperation = 'idle';
        bulkTarget = null;
      }
    }
  }

  function startRename(station: StationInfo) {
    if (stationBusy(station.address) || stationLocked) return;
    editingAddress = station.address;
  }

  function cancelRename() {
    editingAddress = null;
  }

  async function saveRename(station: StationInfo, name: string) {
    if (stationBusy(station.address) || stationLocked) {
      // Keep the row open for a retry and explain why the submission did
      // nothing; a silent rejection leaves the user typing into a dead input.
      const statusOperation = gates.beginStatusOperation();
      const reason = `Rename blocked: another operation is in progress for ${station.name}.`;
      if (gates.canCommitStatus(statusOperation)) statusMessage = reason;
      pushToast(reason, 'warning');
      return;
    }
    cancelRename();
    if (name === station.name) return;
    setConfigBusy(station.address, true);
    const statusOperation = gates.beginStatusOperation();
    const operationEpoch = gates.currentScanEpoch;
    const operationRevision = gates.beginStationOperationRevision(station.address);
    try {
      await RenameStationByAddress(station.address, name);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      stations = stations.map((current) => current.address === station.address
        ? withStationChanges(current, { name: name || current.originalName })
        : current);
      if (gates.canCommitStatus(statusOperation)) statusMessage = name ? `Renamed to ${name}.` : `Reset name for ${station.originalName}.`;
    } catch (error) {
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (gates.canCommitStatus(statusOperation)) {
        statusMessage = `Error renaming: ${error}`;
        pushToast(`Error renaming: ${error}`);
      }
    } finally {
      apiStatus.refresh();
      if (gates.canCleanupStationOperation(station.address, operationRevision)) {
        setConfigBusy(station.address, false);
      }
    }
  }

  async function identify(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    const statusOperation = gates.beginStatusOperation();
    const operationEpoch = gates.currentScanEpoch;
    const operationRevision = gates.beginStationOperationRevision(station.address);
    try {
      await IdentifyStation(station.address);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (gates.canCommitStatus(statusOperation)) statusMessage = `Identify signal sent to ${station.name}.`;
    } catch (error) {
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (gates.canCommitStatus(statusOperation)) {
        statusMessage = `Identify failed for ${station.name}: ${error}`;
        pushToast(`Identify failed for ${station.name}: ${error}`);
      }
    } finally {
      if (gates.canCleanupStationOperation(station.address, operationRevision)) {
        setGattBusy(station.address, false);
      }
    }
  }

  async function refreshCapabilities(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    const statusOperation = gates.beginStatusOperation();
    const operationEpoch = gates.currentScanEpoch;
    const operationRevision = gates.beginStationOperationRevision(station.address);
    try {
      const updated = await RefreshStationCapabilities(station.address);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      stations = stations.map((current) => current.address === station.address ? updated : current);
      if (gates.canCommitStatus(statusOperation)) {
        const message = updated.lastError
          ? `Capabilities refreshed for ${station.name}, but some values are unavailable: ${updated.lastError}`
          : `Capabilities refreshed for ${station.name}.`;
        statusMessage = message;
        if (updated.lastError) pushToast(message, 'warning');
      }
    } catch (error) {
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (gates.canCommitStatus(statusOperation)) {
        statusMessage = `Capability refresh failed for ${station.name}: ${error}`;
        pushToast(`Capability refresh failed for ${station.name}: ${error}`);
      }
    } finally {
      if (gates.canCleanupStationOperation(station.address, operationRevision)) {
        setGattBusy(station.address, false);
      }
    }
  }

  function openChannelEditor(_station: StationInfo) {
    channelError = '';
    channelWarning = false;
    channelEditorOpen = true;
  }

  function closeChannelEditor() {
    if (selectedStation && stationBusy(selectedStation.address)) return;
    channelEditorOpen = false;
  }

  async function saveChannel(targetChannel: number, allowUnknownConflictRisk: boolean) {
    if (!selectedStation) return;
    const blockedReason = channelChangeBlockedReason(selectedStation);
    if (blockedReason) {
      channelError = blockedReason;
      channelWarning = true;
      return;
    }
    if (stationBusy(selectedStation.address) || gattLockedFor(selectedStation.address) ||
      (hasCurrentChannel(selectedStation) && selectedStation.channel === targetChannel)) return;
    const address = selectedStation.address;
    // Capture the display name: the station can drop out of the list while
    // the write is in flight, and the post-await status messages must not
    // dereference a null selectedStation.
    const stationName = selectedStation.name;
    const statusOperation = gates.beginStatusOperation();
    const operationEpoch = gates.currentScanEpoch;
    const operationRevision = gates.beginStationOperationRevision(address);
    setGattBusy(address, true);
    channelError = '';
    channelWarning = false;
    try {
      const result = await SetStationChannel(address, targetChannel, allowUnknownConflictRisk);
      if (!gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      let actual = result.station?.address ? result.station : null;
      if (actual) {
        mergeStationUpdates([actual]);
      } else {
        actual = await fetchStationUpdate(address, operationEpoch, operationRevision);
        if (!gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      }
      if (result.confirmed === false) {
        const warning = result.confirmationError || 'Channel readback is unavailable.';
        channelError = `Channel command sent but unconfirmed: ${warning} Readback: ${channelReadbackLabel(actual)}.`;
        channelWarning = true;
        if (gates.canCommitStatus(statusOperation)) statusMessage = `${stationName}: channel command sent, but confirmation failed. ${warning}`;
        return;
      }
      if (!actual) {
        commitStations(stations.map((station) => station.address === address
          ? withStationChanges(station, { channel: result.channel })
          : station));
      }
      channelEditorOpen = false;
      if (gates.canCommitStatus(statusOperation)) statusMessage = result.commandSent
        ? `Channel changed from ${result.previousChannel || 'unknown'} to ${result.channel}. ${result.warnings.join(' ')}`
        : `Channel already set to ${result.channel}; no command was sent. ${result.warnings.join(' ')}`;
    } catch (error) {
      if (!gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      const actual = await fetchStationUpdate(address, operationEpoch, operationRevision);
      if (!gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      channelError = `${String(error)} Readback: ${channelReadbackLabel(actual)}.`;
      channelWarning = false;
      if (gates.canCommitStatus(statusOperation)) {
        statusMessage = `Channel change failed: ${channelError}`;
        pushToast(statusMessage);
      }
    } finally {
      if (gates.canCleanupStationOperation(address, operationRevision)) {
        setGattBusy(address, false);
      }
    }
  }

  function handleGlobalKeydown(event: KeyboardEvent) {
    if (event.key !== 'Escape') return;
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
    if (channelEditorOpen) {
      closeChannelEditor();
    } else if (bulkConfirmTarget) {
      cancelBulkPower();
    } else if (selectedAddress) {
      selectedAddress = null;
    }
  }
</script>

<svelte:window onkeydown={handleGlobalKeydown} />

<div class="app-container" inert={selectedStation !== null}>
  <AppHeader
    scanning={scanningActive}
    {isBulkLoading}
    {scanLocked}
    {bulkLocked}
    {bulkTarget}
    canOn={actionableOn.length > 0}
    canStandby={actionableStandby.length > 0}
    canSleep={actionableSleep.length > 0}
    allOn={allOn}
    allStandby={allStandby}
    allSleep={allSleep}
    onCount={fleetOn}
    standbyCount={fleetStandby}
    sleepCount={fleetSleep}
    unverifiedCount={fleetUnverified}
    knownCount={stations.length}
    {untrustedCount}
    onScan={handleScanClick}
    onStop={handleStopScan}
    stopping={stoppingScan}
    onBulkPower={handleBulkPower}
  />

  <main>
    {#if conflictStations.length}
      <div class="alert danger" title={conflictDetails} transition:fade={dur({ duration: 180 })}><CircleAlert size={18} /> <span class="alert-text">Channel conflict: {conflictDetails}</span></div>
    {/if}
    {#if scanError && !isLoading && !externalScanning}
      <ScanRecovery
        kind={scanError.kind}
        detail={scanError.detail}
        retryDisabled={scanLocked}
        onRetry={handleScanClick}
      />
    {/if}
    {#if sortedStations.length}
      <ChannelMap stations={sortedStations} channelOf={displayChannel} selectedAddress={selectedAddress} onSelect={(address) => selectedAddress = address} />
      <div class="station-grid">
        {#each sortedStations as station, index (station.address)}
          <div
            animate:flip={dur({ duration: 300, easing: cubicOut })}
            in:fade={dur({ duration: 180, delay: Math.min(index * 30, 240) })}
            out:fade={dur({ duration: 120 })}
          >
            <StationCard
              {station}
              channelDisplay={displayChannel(station)}
              renaming={editingAddress === station.address}
              feedback={powerFeedbackMap[station.address]}
              pendingTarget={powerTargetByAddress[station.address]}
              gattBusy={gattOperations.has(station.address)}
              configBusy={configOperations.has(station.address)}
              gattLocked={gattLockedByAddress.get(station.address) ?? false}
              renameLocked={stationLocked}
              onPower={setPower}
              onOpenDetails={(s) => selectedAddress = s.address}
              onStartRename={startRename}
              onSaveRename={saveRename}
              onCancelRename={cancelRename}
            />
          </div>
        {/each}
      </div>
    {:else if isLoading || externalScanning}
      <div class="empty scan" in:fade={dur({ duration: 180 })}>
        <div class="empty-icon"><Radar size={40} /></div>
        <p>{isLoading ? 'Scanning for base stations...' : 'External scan in progress...'}{scanElapsed >= 1 ? ` ${scanElapsed}s` : ''}</p>
        <p class="scan-sub">{scanElapsed >= 6 ? 'Reading station states...' : 'Discovering nearby stations...'}</p>
      </div>
    {:else if !scanError}
      <div class="empty">
        <div class="empty-icon"><Activity size={40} /></div>
        <p>No base stations found.</p>
        <button class="btn primary" disabled={scanLocked} onclick={handleScanClick}>
          Scan Now
        </button>
      </div>
    {/if}
  </main>

  <StatusFooter {statusMessage} {apiRunning} {apiError} {apiAddress} {configWarnings} {configWritable} />
</div>

{#if selectedStation}
  <div
    class="scrim"
    role="presentation"
    transition:fade={dur({ duration: 200 })}
    onclick={() => selectedAddress = null}
  ></div>
  <DetailsDrawer
    station={selectedStation}
    busy={busyByAddress.get(selectedStation.address) ?? false}
    locked={gattLockedByAddress.get(selectedStation.address) ?? false}
    inactive={channelEditorOpen}
    onClose={() => selectedAddress = null}
    onRefresh={refreshCapabilities}
    onIdentify={identify}
    onOpenChannelEditor={openChannelEditor}
  />
{/if}

{#if channelEditorOpen && selectedStation}
  <div
    class="modal-scrim"
    role="presentation"
    transition:fade={dur({ duration: 180 })}
    onclick={closeChannelEditor}
  >
    <ChannelModal
      station={selectedStation}
      {occupiedChannels}
      {hasUnknownVisibleChannel}
      error={channelError}
      warning={channelWarning}
      busy={gattOperations.has(selectedStation.address) || configOperations.has(selectedStation.address)}
      locked={gattLockedByAddress.get(selectedStation.address) ?? false}
      onClose={closeChannelEditor}
      onSave={saveChannel}
      onIdentify={identify}
    />
  </div>
{/if}

{#if bulkConfirmTarget}
  <div
    class="modal-scrim"
    role="presentation"
    transition:fade={dur({ duration: 180 })}
    onclick={cancelBulkPower}
  >
    <BulkConfirmModal
      target={bulkConfirmTarget}
      {visibleCount}
      {invisibleCount}
      {uncertainCount}
      actionableCount={bulkConfirmTarget === 'on' ? actionableOn.length : bulkConfirmTarget === 'standby' ? actionableStandby.length : actionableSleep.length}
      busy={isBulkLoading || bulkLocked || scanningActive}
      onCancel={cancelBulkPower}
      onConfirm={confirmBulkPower}
    />
  </div>
{/if}

<Toast />

<style>
  .app-container { display: flex; flex-direction: column; height: 100vh; }
  main { flex: 1; padding: var(--spacing-md) var(--spacing-md) var(--spacing-lg); overflow: auto; }
  .station-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: var(--spacing-md);
    /* Stop cards from stretching into sparse wide stripes on ultra-wide
       windows. */
    max-width: 1720px;
    margin-inline: auto;
    width: 100%;
  }
  .scrim {
    position: fixed;
    inset: 0;
    background: rgba(16, 24, 40, 0.38);
    backdrop-filter: blur(3px);
    z-index: 10;
  }
  .modal-scrim {
    position: fixed;
    inset: 0;
    background: rgba(16, 24, 40, 0.42);
    backdrop-filter: blur(3px);
    z-index: 20;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 1rem;
  }
  .empty {
    height: 100%;
    display: flex;
    flex-direction: column;
    justify-content: center;
    align-items: center;
    gap: 0.7rem;
    color: var(--text-muted);
  }
  .empty-icon {
    position: relative;
    width: 92px;
    height: 92px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius-pill);
    border: 1px solid color-mix(in srgb, var(--color-primary) 25%, transparent);
    background:
      linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 10%, white), color-mix(in srgb, var(--color-sleep) 12%, white));
    color: var(--color-primary-deep);
    box-shadow: 0 14px 34px -14px color-mix(in srgb, var(--color-primary) 45%, transparent), var(--shadow-sm);
  }
  .empty-icon::after {
    content: '';
    position: absolute;
    inset: -1px;
    border-radius: var(--radius-pill);
    border: 1px solid color-mix(in srgb, var(--color-primary) 40%, transparent);
  }
  /* Only a genuinely running scan pulses; the idle "no stations" state keeps
     a static ring so it does not suggest activity that is not happening. */
  .empty.scan .empty-icon::after {
    animation: ping-ring 2.2s var(--ease) infinite;
  }
  @keyframes ping-ring {
    0% { transform: scale(0.9); opacity: 1; }
    70% { transform: scale(1.35); opacity: 0; }
    100% { transform: scale(1.35); opacity: 0; }
  }
  .empty p { margin: 0; font-size: 0.85rem; font-weight: 700; }
  .empty .scan-sub { font-size: var(--fs-sm); font-weight: 600; color: var(--text-muted); }
</style>
