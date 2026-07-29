<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { flip } from 'svelte/animate';
  import { cubicOut } from 'svelte/easing';
  import { fade } from 'svelte/transition';
  import {
    CheckAllStationStatuses,
    GetAPIStatus,
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
  } from '../wailsjs/go/main/App';
  import { EventsOn } from '../wailsjs/runtime/runtime';
  import { station as stationModels } from '../wailsjs/go/models';
  import { Activity, CircleAlert, Radar } from 'lucide-svelte';
  import type { PowerFeedback, PowerTarget, StationInfo } from './lib/types';
  import {
    canSetPower, channelChangeBlockedReason, hasCurrentChannel, hasVerifiedPowerState, isCurrentPowerState,
    powerTargetLabel, stateLabel
  } from './lib/station';
  import { formatBulkResult, formatTerminalScanResult, summarizeBulkResult } from './lib/result-format';
  import { clearToasts, pushToast } from './lib/toast';
  import { deriveOperationLocks, type GlobalOperation } from './lib/operation-state';
  import { RevisionGate } from './lib/revision-gate';
  import AppHeader from './lib/components/AppHeader.svelte';
  import StationCard from './lib/components/StationCard.svelte';
  import ChannelMap from './lib/components/ChannelMap.svelte';
  import DetailsDrawer from './lib/components/DetailsDrawer.svelte';
  import ChannelModal from './lib/components/ChannelModal.svelte';
  import StatusFooter from './lib/components/StatusFooter.svelte';
  import Toast from './lib/components/Toast.svelte';

  let stations: StationInfo[] = [];
  let statusMessage = 'Ready to scan.';
  let statusEpoch = 0;
  let gattOperations = new Set<string>();
  let configOperations = new Set<string>();
  let powerTargetByAddress: Record<string, PowerTarget | undefined> = {};
  let powerFeedbackByAddress: Record<string, PowerFeedback | undefined> = {};
  let globalOperation: GlobalOperation = 'idle';
  let bulkTarget: PowerTarget | null = null;
  let editingAddress: string | null = null;
  let selectedAddress: string | null = null;
  let channelEditorOpen = false;
  let channelError = '';
  let channelWarning = false;
  let statusCheckInterval: ReturnType<typeof setInterval> | null = null;
  let apiStatusInterval: ReturnType<typeof setInterval> | null = null;
  let cancelExternalScanListener: (() => void) | null = null;
  let cancelExternalScanFailureListener: (() => void) | null = null;
  let cancelExternalScanStartedListener: (() => void) | null = null;
  let cancelExternalScanCancelledListener: (() => void) | null = null;
  let apiRunning = false;
  let apiError = '';
  let configWarnings: string[] = [];
  let configWritable = true;
  const reportedConfigWarnings = new Set<string>();
  let externalScanning = false;
  let externalScanID: number | null = null;
  let latestExternalScanID = 0;
  let externalScanRecoveryEpoch: number | null = null;
  let externalScanRecoveryStatusEpoch: number | null = null;
  let stoppingScan = false;
  let stopRequestPending = false;
  let stopRequestGeneration = 0;
  let scanStartedAt: number | null = null;
  let scanElapsed = 0;
  let scanTimer: ReturnType<typeof setInterval> | null = null;
  const listRevisions = new RevisionGate();
  const apiRevisions = new RevisionGate();
  let scanEpoch = 0;
  let nextStationRevision = 0;
  let stationRevisions = new Map<string, number>();
  let disposed = false;
  let startupPending = true;

  interface ExternalScanEvent {
    id: number;
    stations?: StationInfo[];
    error?: string;
  }

  async function matchesUnknownExternalScan(event: ExternalScanEvent): Promise<boolean> {
    // The app may have attached after the start event. In that case adopt the
    // first terminal ID only after confirming the backend scan has ended.
    if (!externalScanning || externalScanID !== null) return false;
    const epoch = scanEpoch;
    const scanning = await IsScanning().catch(() => true);
    return externalScanning && externalScanID === null && scanEpoch === epoch && !scanning;
  }

  function beginExternalScan(id: number | null) {
    beginScanEpoch();
    listRevisions.next();
    prepareForScan(false);
    externalScanRecoveryEpoch = null;
    externalScanRecoveryStatusEpoch = null;
    externalScanID = id;
    externalScanning = true;
    stoppingScan = false;
    beginScanTimer();
    statusMessage = 'Preparing external scan...';
  }

  function claimExternalScanTerminal(event: ExternalScanEvent): boolean {
    if (!externalScanning) return false;
    if (!externalScanning || (externalScanID !== null && event.id !== externalScanID)) return false;
    latestExternalScanID = Math.max(latestExternalScanID, event.id);
    externalScanning = false;
    externalScanID = null;
    return true;
  }

  async function claimUnknownExternalScanTerminal(event: ExternalScanEvent): Promise<boolean> {
    if (event.id <= latestExternalScanID || !externalScanning || externalScanID !== null ||
      !(await matchesUnknownExternalScan(event))) return false;
    // An adopted scan has no ID until its terminal event. Claim it before any
    // further awaits so concurrent terminal notifications cannot both commit.
    return claimExternalScanTerminal(event);
  }

  function beginStatusOperation(): number {
    return ++statusEpoch;
  }

  function canCommitStatus(epoch: number): boolean {
    return !disposed && epoch === statusEpoch;
  }

  $: sortedStations = [...stations].sort((a, b) => {
    const ac = a.channel > 0 ? a.channel : Number.MAX_SAFE_INTEGER;
    const bc = b.channel > 0 ? b.channel : Number.MAX_SAFE_INTEGER;
    return ac - bc || a.name.localeCompare(b.name) || a.address.localeCompare(b.address);
  });
  $: selectedStation = stations.find((station) => station.address === selectedAddress) ?? null;
  $: conflictStations = stations.filter((station) => station.channelConflict);
  $: conflictDetails = (() => {
    const byChannel = new Map<number, string[]>();
    for (const station of conflictStations) {
      const key = station.channel > 0 ? station.channel : -1;
      byChannel.set(key, [...(byChannel.get(key) ?? []), station.name]);
    }
    return [...byChannel.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([channel, names]) => channel > 0 ? `CH ${channel}: ${names.join(' + ')}` : names.join(' + '))
      .join(' · ');
  })();
  $: fleetOn = stations.filter((station) => hasVerifiedPowerState(station, 'on')).length;
  $: fleetStandby = stations.filter((station) => hasVerifiedPowerState(station, 'standby')).length;
  $: fleetSleep = stations.filter((station) => hasVerifiedPowerState(station, 'sleep')).length;
  $: actionableOn = stations.filter((station) => canSetPower(station, 'on'));
  $: actionableStandby = stations.filter((station) => canSetPower(station, 'standby'));
  $: actionableSleep = stations.filter((station) => canSetPower(station, 'sleep'));
  $: occupiedChannels = (() => {
    const occupied = new Map<number, string[]>();
    const candidates = stations
      .filter((station) => hasCurrentChannel(station) && station.address !== selectedAddress)
      .sort((a, b) => a.name.localeCompare(b.name) || a.address.localeCompare(b.address));
    for (const station of candidates) {
      occupied.set(station.channel, [...(occupied.get(station.channel) ?? []), station.name]);
    }
    return occupied;
  })();
  $: hasUnknownVisibleChannel = stations.some(
    (station) => station.isPresent && station.address !== selectedAddress &&
      (!station.scanFresh || !station.channelFresh || station.channel === 0)
  );
  $: operationLocks = deriveOperationLocks({
    global: globalOperation,
    externalScanning,
    gattAddresses: gattOperations,
    configAddresses: configOperations
  });
  $: isLoading = globalOperation === 'scanning';
  $: isBulkLoading = globalOperation === 'bulk-power';
  $: isStatusChecking = globalOperation === 'status-refresh';
  $: scanningActive = isLoading || externalScanning || stoppingScan;
  $: anyDeviceOperation = operationLocks.anyDeviceOperation;
  $: scanLocked = operationLocks.scanLocked;
  $: bulkLocked = operationLocks.bulkLocked;
  $: stationLocked = operationLocks.stationLocked;
  // The periodic status reader occupies one of the backend's two device
  // operation slots. Keep the remaining slot available to one foreground
  // action instead of allowing a second action that will fail with Busy.
  $: foregroundGattCapacity = isStatusChecking ? 1 : 2;
  $: gattCapacityReached = gattOperations.size >= foregroundGattCapacity;

  function stationBusy(address: string): boolean {
    return gattOperations.has(address) || configOperations.has(address);
  }

  function beginScanTimer() {
    if (scanTimer) return;
    scanStartedAt = Date.now();
    scanElapsed = 0;
    scanTimer = setInterval(() => {
      if (scanStartedAt !== null) scanElapsed = Math.floor((Date.now() - scanStartedAt) / 1000);
    }, 1000);
  }

  function endScanTimer() {
    if (scanTimer) {
      clearInterval(scanTimer);
      scanTimer = null;
    }
    scanStartedAt = null;
    scanElapsed = 0;
  }

  function maybeEndScanTimer() {
    // A local scan and an external scan can overlap; the timer belongs to
    // whichever is still running, so only stop when both have finished.
    if (globalOperation !== 'scanning' && !externalScanning) endScanTimer();
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

  function gattLockedFor(address: string): boolean {
    return stationLocked || (gattCapacityReached && !gattOperations.has(address));
  }

  function stationRevision(address: string): number {
    return stationRevisions.get(address) ?? 0;
  }

  function beginStationOperationRevision(address: string): number {
    const revision = ++nextStationRevision;
    stationRevisions = new Map(stationRevisions).set(address, revision);
    return revision;
  }

  function canCommitStationOperation(epoch: number, address: string, revision: number): boolean {
    return canCommitOperation(epoch) && stationRevision(address) === revision;
  }

  function applyStationList(
    updated: StationInfo[] | null | undefined,
    revision: number,
    capturedStationRevisions?: Map<string, number>
  ): boolean {
    if (disposed || !listRevisions.isCurrent(revision)) return false;
    const incoming = updated || [];
    if (!capturedStationRevisions) {
      stations = incoming;
      return true;
    }
    const currentByAddress = new Map(stations.map((station) => [station.address, station]));
    const incomingAddresses = new Set(incoming.map((station) => station.address));
    const merged = incoming.map((station) =>
      stationRevision(station.address) === (capturedStationRevisions.get(station.address) ?? 0)
        ? station
        : currentByAddress.get(station.address) ?? station
    );
    for (const current of stations) {
      if (!incomingAddresses.has(current.address) &&
        stationRevision(current.address) !== (capturedStationRevisions.get(current.address) ?? 0)) {
        merged.push(current);
      }
    }
    stations = merged;
    return true;
  }

  function canCommitOperation(epoch: number): boolean {
    return !disposed && epoch === scanEpoch;
  }

  function beginScanEpoch(): number {
    scanEpoch += 1;
    stationRevisions = new Map();
    return scanEpoch;
  }

  function prepareForScan(clearOperations = true) {
    cancelRename();
    channelEditorOpen = false;
    channelError = '';
    channelWarning = false;
    // A locally running scan has acquired the backend's exclusive operation
    // lock. An accepted external scan may still be waiting for existing work,
    // so preserve those visible operations until their promises settle.
    if (clearOperations) {
      gattOperations = new Set();
      configOperations = new Set();
    }
    powerTargetByAddress = {};
    powerFeedbackByAddress = {};
  }

  function mergeStationUpdates(updated: StationInfo[]) {
    if (!updated.length) return;
    const byAddress = new Map(updated.map((station) => [station.address, station]));
    const existingAddresses = new Set(stations.map((station) => station.address));
    stations = [
      ...stations.map((station) => byAddress.get(station.address) ?? station),
      ...updated.filter((station) => !existingAddresses.has(station.address))
    ];
  }

  function withStationChanges(current: StationInfo, changes: Partial<StationInfo>): StationInfo {
    return stationModels.StationInfo.createFrom({ ...current, ...changes });
  }

  onMount(async () => {
    const startupScanEpoch = scanEpoch;
    refreshAPIStatus();
    apiStatusInterval = setInterval(refreshAPIStatus, 15000);
    cancelExternalScanStartedListener = EventsOn('external-scan-started', (value: unknown) => {
      if (disposed) return;
      const event = value as ExternalScanEvent;
      if (event.id <= latestExternalScanID) return;
      latestExternalScanID = event.id;
      beginExternalScan(event.id);
    });
    cancelExternalScanListener = EventsOn('external-scan-completed', async (value: unknown) => {
      const event = value as ExternalScanEvent;
      if (externalScanID === null
        ? !(await claimUnknownExternalScanTerminal(event))
        : !claimExternalScanTerminal(event)) return;
      const statusOperation = beginStatusOperation();
      const operationEpoch = beginScanEpoch();
      const revision = listRevisions.next();
      prepareForScan();
      externalScanRecoveryEpoch = operationEpoch;
      externalScanRecoveryStatusEpoch = statusOperation;
      stoppingScan = false;
      maybeEndScanTimer();
      const capturedStationRevisions = new Map(stationRevisions);
      const scanStatus = await GetScanStatus().catch(() => null);
      if (disposed || !listRevisions.isCurrent(revision)) return;
      if (scanStatus) {
        externalScanRecoveryEpoch = null;
        externalScanRecoveryStatusEpoch = null;
      }
      if (!applyStationList(event.stations || [], revision, capturedStationRevisions)) return;
      if (!canCommitStatus(statusOperation)) return;
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = formatTerminalScanResult({
        state: 'completed', found, known: stations.length, warnings: scanStatus?.warnings, external: true
      });
    });
    cancelExternalScanFailureListener = EventsOn('external-scan-failed', async (value: unknown) => {
      const event = value as ExternalScanEvent;
      if (externalScanID === null
        ? !(await claimUnknownExternalScanTerminal(event))
        : !claimExternalScanTerminal(event)) return;
      const statusOperation = beginStatusOperation();
      const message = event.error || 'unknown error';
      const operationEpoch = beginScanEpoch();
      const revision = listRevisions.next();
      prepareForScan();
      externalScanRecoveryEpoch = operationEpoch;
      externalScanRecoveryStatusEpoch = statusOperation;
      if (globalOperation !== 'scanning') stoppingScan = false;
      maybeEndScanTimer();
      const capturedStationRevisions = new Map(stationRevisions);
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (updated) {
        applyStationList(updated, revision, capturedStationRevisions);
      }
      const scanStatus = await GetScanStatus().catch(() => null);
      if (disposed || !listRevisions.isCurrent(revision)) return;
      if (updated && scanStatus) {
        externalScanRecoveryEpoch = null;
        externalScanRecoveryStatusEpoch = null;
      }
      if (!canCommitStatus(statusOperation)) return;
      statusMessage = formatTerminalScanResult({
        state: 'failed', error: message, known: stations.length,
        warnings: scanStatus?.warnings, external: true
      });
      pushToast(`External scan failed: ${message}`);
    });
    cancelExternalScanCancelledListener = EventsOn('external-scan-cancelled', async (value: unknown) => {
      const event = value as ExternalScanEvent;
      if (externalScanID === null
        ? !(await claimUnknownExternalScanTerminal(event))
        : !claimExternalScanTerminal(event)) return;
      const statusOperation = beginStatusOperation();
      const operationEpoch = beginScanEpoch();
      const revision = listRevisions.next();
      prepareForScan();
      externalScanRecoveryEpoch = operationEpoch;
      externalScanRecoveryStatusEpoch = statusOperation;
      if (globalOperation !== 'scanning') stoppingScan = false;
      maybeEndScanTimer();
      const capturedStationRevisions = new Map(stationRevisions);
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (updated) {
        applyStationList(updated, revision, capturedStationRevisions);
      }
      if (!canCommitStatus(statusOperation)) return;
      statusMessage = 'Scan stopped.';
    });
    statusCheckInterval = setInterval(periodicStatusCheck, 15000);
    const startupScanning = await IsScanning().catch(() => false);
    // Do not allow the first polling tick to acquire the backend operation
    // lock before the initial external-scan check can start the local scan.
    startupPending = false;
    // An external scan event may have arrived while this initial query was
    // pending. Do not let its older result overwrite the newer event state.
    if (disposed || startupScanEpoch !== scanEpoch) return;
    if (startupScanning) {
      beginExternalScan(null);
    } else {
      await handleScanClick();
    }
  });

  function refreshAPIStatus() {
    const revision = apiRevisions.next();
    GetAPIStatus().then((status) => {
      if (disposed || !apiRevisions.isCurrent(revision)) return;
      apiRunning = status.running;
      apiError = status.error;
      configWarnings = status.warnings ?? [];
      configWritable = status.configWritable ?? true;
      const currentWarnings = new Set(configWarnings);
      for (const warning of reportedConfigWarnings) {
        if (!currentWarnings.has(warning)) reportedConfigWarnings.delete(warning);
      }
      for (const warning of configWarnings) {
        if (reportedConfigWarnings.has(warning)) continue;
        reportedConfigWarnings.add(warning);
        pushToast(warning, 'warning');
      }
    }).catch((error) => {
      if (disposed || !apiRevisions.isCurrent(revision)) return;
      apiRunning = false;
      apiError = String(error);
    });
  }

  onDestroy(() => {
    disposed = true;
    clearToasts();
    listRevisions.dispose();
    apiRevisions.dispose();
    endScanTimer();
    if (statusCheckInterval) clearInterval(statusCheckInterval);
    if (apiStatusInterval) clearInterval(apiStatusInterval);
    cancelExternalScanListener?.();
    cancelExternalScanFailureListener?.();
    cancelExternalScanStartedListener?.();
    cancelExternalScanCancelledListener?.();
  });

  async function periodicStatusCheck() {
    if (startupPending || isStatusChecking || isLoading || isBulkLoading || anyDeviceOperation || editingAddress !== null) return;
    globalOperation = 'status-refresh';
    const statusOperation = statusEpoch;
    const revision = listRevisions.next();
    const capturedStationRevisions = new Map(stationRevisions);
    try {
      const scanning = await IsScanning();
      if (disposed || !listRevisions.isCurrent(revision)) return;
      const wasExternalScanning = externalScanning;
      if (scanning && !isLoading && !externalScanning) beginExternalScan(null);
      else externalScanning = scanning && !isLoading;
      if (!scanning) {
        stoppingScan = false;
        if (wasExternalScanning) {
          externalScanRecoveryEpoch = scanEpoch;
          externalScanRecoveryStatusEpoch = statusEpoch;
        }
        if (externalScanRecoveryEpoch === scanEpoch) {
          if (!applyStationList(await GetCurrentStationInfo(), revision, capturedStationRevisions)) {
            return;
          }
          const scanStatus = await GetScanStatus().catch(() => null);
          if (disposed || !listRevisions.isCurrent(revision)) return;
          if (!scanStatus) {
            return;
          }
          externalScanRecoveryEpoch = null;
          const canWriteTerminalStatus = externalScanRecoveryStatusEpoch === statusOperation && canCommitStatus(statusOperation);
          externalScanRecoveryStatusEpoch = null;
          if (!canWriteTerminalStatus) return;
          const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
          statusMessage = formatTerminalScanResult({
            state: scanStatus?.state ?? 'completed',
            found, known: stations.length,
            error: scanStatus?.error,
            warnings: scanStatus?.warnings,
            external: true
          });
        } else if (!applyStationList(await CheckAllStationStatuses(), revision, capturedStationRevisions)) return;
        maybeEndScanTimer();
      } else {
        beginScanTimer();
        if (externalScanning) {
          const scanStatus = await GetScanStatus().catch(() => null);
          if (!disposed && listRevisions.isCurrent(revision) && scanStatus) {
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
      if (canCommitStatus(statusOperation)) statusMessage = `Status refresh incomplete: ${error}`;
    } finally {
      if (!disposed && globalOperation === 'status-refresh') globalOperation = 'idle';
    }
  }

  async function handleScanClick() {
    if (isLoading || scanLocked) return;
    prepareForScan();
    globalOperation = 'scanning';
    externalScanID = null;
    externalScanRecoveryEpoch = null;
    externalScanRecoveryStatusEpoch = null;
    const statusOperation = beginStatusOperation();
    beginScanTimer();
    const operationEpoch = beginScanEpoch();
    const revision = listRevisions.next();
    statusMessage = 'Scanning for base stations...';
    try {
      if (!applyStationList(await ScanAndFetchStations(), revision)) return;
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision) || !canCommitStatus(statusOperation)) return;
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = formatTerminalScanResult({
        state: scanStatus?.state ?? 'completed',
        found, known: stations.length,
        error: scanStatus?.error,
        warnings: scanStatus?.warnings
      });
    } catch (error) {
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision) || !canCommitStatus(statusOperation)) return;
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (updated) applyStationList(updated, revision);
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision) || !canCommitStatus(statusOperation)) return;
      if (stoppingScan || scanStatus?.state === 'cancelled') {
        if (!stopRequestPending) stoppingScan = false;
        statusMessage = 'Scan stopped.';
      } else {
        statusMessage = `Scan failed: ${error}`;
        pushToast(`Scan failed: ${error}`);
      }
    } finally {
      if (!disposed && globalOperation === 'scanning') globalOperation = 'idle';
      if (!disposed && !stopRequestPending && !externalScanning) stoppingScan = false;
      maybeEndScanTimer();
    }
  }

  async function handleStopScan() {
    if (!scanningActive || stoppingScan) return;
    const operationEpoch = scanEpoch;
    const requestGeneration = ++stopRequestGeneration;
    stoppingScan = true;
    stopRequestPending = true;
    statusMessage = 'Stopping scan...';
    try {
      await StopScan();
      if (!canCommitOperation(operationEpoch)) return;
      stopRequestPending = false;
      if (!stoppingScan) return;
      if (externalScanning) {
        const stillScanning = await IsScanning().catch(() => true);
        if (!canCommitOperation(operationEpoch) || stopRequestGeneration !== requestGeneration) return;
        if (stillScanning) {
          statusMessage = 'Stopping scan...';
        } else {
          externalScanning = false;
          externalScanID = null;
          stoppingScan = false;
          externalScanRecoveryEpoch = scanEpoch;
          externalScanRecoveryStatusEpoch = statusEpoch;
          const revision = listRevisions.next();
          const capturedStationRevisions = new Map(stationRevisions);
          const updated = await GetCurrentStationInfo().catch(() => null);
          if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
          if (!updated || !applyStationList(updated, revision, capturedStationRevisions)) return;
          const scanStatus = await GetScanStatus().catch(() => null);
          if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
          if (!scanStatus) return;
          externalScanRecoveryEpoch = null;
          const canWriteTerminalStatus = externalScanRecoveryStatusEpoch === statusEpoch;
          externalScanRecoveryStatusEpoch = null;
          if (canWriteTerminalStatus) {
            const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
            statusMessage = formatTerminalScanResult({
              state: scanStatus?.state ?? 'cancelled', found, known: stations.length,
              error: scanStatus?.error, warnings: scanStatus?.warnings, external: true
            });
          }
          maybeEndScanTimer();
        }
      } else {
        if (globalOperation !== 'scanning') stoppingScan = false;
        statusMessage = 'Scan stopped.';
        maybeEndScanTimer();
      }
    } catch (error) {
      if (!canCommitOperation(operationEpoch)) return;
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
    const capturedStationRevisions = new Map(stationRevisions);
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
    if (!canCommitStationOperation(epoch, address, operationRevision)) return null;
    const station = updated?.find((item) => item.address === address) ?? null;
    if (station) {
      stations = stations.map((current) => current.address === address ? station : current);
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
    const operationEpoch = scanEpoch;
    const statusOperation = beginStatusOperation();
    const operationRevision = beginStationOperationRevision(station.address);
    setGattBusy(station.address, true);
    powerTargetByAddress = { ...powerTargetByAddress, [station.address]: state };
    powerFeedbackByAddress = {
      ...powerFeedbackByAddress,
      [station.address]: { kind: 'pending', text: `Switching to ${targetLabel}…` }
    };
    statusMessage = `Setting ${station.name} to ${targetLabel}…`;
    try {
      const result = await SetStationPower(station.address, state);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      stations = stations.map((current) => current.address === station.address ? result.station : current);
      powerFeedbackByAddress = {
        ...powerFeedbackByAddress,
        [station.address]: result.confirmed
          ? { kind: 'success', text: `${targetLabel} confirmed` }
          : { kind: 'warning', text: result.confirmationError
              ? `${targetLabel} sent · confirmation failed`
              : `${targetLabel} sent · status unavailable` }
      };
      if (canCommitStatus(statusOperation)) {
        statusMessage = result.confirmed
          ? `${station.name} is ${targetLabel}.`
          : result.confirmationError
            ? `${station.name}: command sent, but confirmation failed. ${result.confirmationError}`
            : `${station.name}: ${targetLabel} command sent; this firmware cannot confirm the state.`;
      }
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      const errorText = String(error);
      const actual = await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      powerFeedbackByAddress = {
        ...powerFeedbackByAddress,
        [station.address]: { kind: 'error', text: `Failed · ${powerReadbackLabel(actual)}` }
      };
      if (canCommitStatus(statusOperation)) {
        statusMessage = `Power change failed for ${station.name}: ${errorText}`;
        pushToast(`Power change failed for ${station.name}: ${errorText}`);
      }
    } finally {
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
        powerTargetByAddress = { ...powerTargetByAddress, [station.address]: undefined };
        setGattBusy(station.address, false);
      }
    }
  }

  function actionablePowerStations(state: PowerTarget): StationInfo[] {
    return stations.filter((station) => canSetPower(station, state));
  }

  function allStationsAtState(state: PowerTarget): boolean {
    return stations.length > 0 &&
      stations.every((station) => isCurrentPowerState(station, state));
  }

  async function handleBulkPower(state: PowerTarget) {
    // Do not duplicate backend capability/state decisions here. Cached
    // frontend data can be stale after scanning, while the backend refreshes
    // capabilities and returns a result for every known station.
    if (bulkLocked || actionablePowerStations(state).length === 0) return;
    globalOperation = 'bulk-power';
    const statusOperation = beginStatusOperation();
    bulkTarget = state;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = scanEpoch;
    statusMessage = `Setting all available stations to ${targetLabel}…`;
    try {
      const result = await SetAllStationsPowerDetailed(state);
      if (!canCommitOperation(operationEpoch)) return;
      mergeStationUpdates(result.results.map((item) => item.station).filter((item) => Boolean(item?.address)));
      await fetchLatestList();
      if (!canCommitOperation(operationEpoch)) return;
      const feedback = { ...powerFeedbackByAddress };
      for (const item of result.results) {
        feedback[item.address] = item.skipped
          ? item.success && item.confirmed
            ? { kind: 'success', text: `Already ${targetLabel}` }
            : { kind: 'warning', text: `Skipped · ${item.reason || 'not actionable'}` }
            : item.success && item.confirmed
              ? { kind: 'success', text: `${targetLabel} confirmed` }
              : item.success && item.commandSent
              ? { kind: 'warning', text: `${targetLabel} sent · ${item.error || 'status unavailable'}` }
              : { kind: 'error', text: item.error || `Failed to set ${targetLabel}` };
      }
      powerFeedbackByAddress = feedback;
      const summary = summarizeBulkResult(result.results);
      if (!canCommitStatus(statusOperation)) return;
      statusMessage = formatBulkResult(targetLabel, summary);
      const toastKind = summary.failed.length ? 'error'
        : summary.unconfirmed || summary.skipped ? 'warning'
          : 'success';
      pushToast(`Bulk ${targetLabel}: ${formatBulkResult(targetLabel, summary)}`, toastKind);
    } catch (error) {
      if (!canCommitOperation(operationEpoch)) return;
      await fetchLatestList();
      if (!canCommitOperation(operationEpoch)) return;
      if (canCommitStatus(statusOperation)) {
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
      return;
    }
    cancelRename();
    if (name === station.name) return;
    setConfigBusy(station.address, true);
    const statusOperation = beginStatusOperation();
    const operationEpoch = scanEpoch;
    const operationRevision = beginStationOperationRevision(station.address);
    try {
      await RenameStationByAddress(station.address, name);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      stations = stations.map((current) => current.address === station.address
        ? withStationChanges(current, { name: name || current.originalName })
        : current);
      if (canCommitStatus(statusOperation)) statusMessage = name ? `Renamed to ${name}.` : `Reset name for ${station.originalName}.`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (canCommitStatus(statusOperation)) {
        statusMessage = `Error renaming: ${error}`;
        pushToast(`Error renaming: ${error}`);
      }
    } finally {
      refreshAPIStatus();
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
        setConfigBusy(station.address, false);
      }
    }
  }

  async function identify(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    const statusOperation = beginStatusOperation();
    const operationEpoch = scanEpoch;
    const operationRevision = beginStationOperationRevision(station.address);
    try {
      await IdentifyStation(station.address);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (canCommitStatus(statusOperation)) statusMessage = `Identify signal sent to ${station.name}.`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (canCommitStatus(statusOperation)) {
        statusMessage = `Identify failed for ${station.name}: ${error}`;
        pushToast(`Identify failed for ${station.name}: ${error}`);
      }
    } finally {
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
        setGattBusy(station.address, false);
      }
    }
  }

  async function refreshCapabilities(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    const statusOperation = beginStatusOperation();
    const operationEpoch = scanEpoch;
    const operationRevision = beginStationOperationRevision(station.address);
    try {
      const updated = await RefreshStationCapabilities(station.address);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      stations = stations.map((current) => current.address === station.address ? updated : current);
      if (canCommitStatus(statusOperation)) {
        const message = updated.lastError
          ? `Capabilities refreshed for ${station.name}, but some values are unavailable: ${updated.lastError}`
          : `Capabilities refreshed for ${station.name}.`;
        statusMessage = message;
        if (updated.lastError) pushToast(message, 'warning');
      }
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (canCommitStatus(statusOperation)) {
        statusMessage = `Capability refresh failed for ${station.name}: ${error}`;
        pushToast(`Capability refresh failed for ${station.name}: ${error}`);
      }
    } finally {
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
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
    const statusOperation = beginStatusOperation();
    const operationEpoch = scanEpoch;
    const operationRevision = beginStationOperationRevision(address);
    setGattBusy(address, true);
    channelError = '';
    channelWarning = false;
    try {
      const result = await SetStationChannel(address, targetChannel, allowUnknownConflictRisk) as Awaited<ReturnType<typeof SetStationChannel>> & {
        commandSent?: boolean;
        confirmed?: boolean;
        confirmationError?: string;
      };
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      const actual = await fetchStationUpdate(address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      if (result.confirmed === false) {
        const warning = result.confirmationError || 'Channel readback is unavailable.';
        channelError = `Channel command sent but unconfirmed: ${warning} Readback: ${channelReadbackLabel(actual)}.`;
        channelWarning = true;
        if (canCommitStatus(statusOperation)) statusMessage = `${selectedStation.name}: channel command sent, but confirmation failed. ${warning}`;
        return;
      }
      const authoritative = result.station?.address ? result.station : actual;
      if (authoritative) {
        stations = stations.map((station) => station.address === address ? authoritative : station);
      } else {
        stations = stations.map((station) => station.address === address
          ? withStationChanges(station, { channel: result.channel })
          : station);
      }
      channelEditorOpen = false;
      if (canCommitStatus(statusOperation)) statusMessage = `Channel changed from ${result.previousChannel || 'unknown'} to ${result.channel}. ${result.warnings.join(' ')}`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      const actual = await fetchStationUpdate(address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      channelError = `${String(error)} Readback: ${channelReadbackLabel(actual)}.`;
      channelWarning = false;
      if (canCommitStatus(statusOperation)) {
        statusMessage = `Channel change failed: ${channelError}`;
        pushToast(statusMessage);
      }
    } finally {
      if (canCommitStationOperation(operationEpoch, address, operationRevision)) {
        setGattBusy(address, false);
      }
    }
  }

  function handleGlobalKeydown(event: KeyboardEvent) {
    if (event.key !== 'Escape') return;
    if (channelEditorOpen) {
      closeChannelEditor();
    } else if (selectedAddress) {
      selectedAddress = null;
    }
  }
</script>

<svelte:window on:keydown={handleGlobalKeydown} />

<div class="app-container" inert={selectedStation !== null} aria-hidden={selectedStation ? 'true' : undefined}>
  <AppHeader
    scanning={scanningActive}
    {isBulkLoading}
    {scanLocked}
    {bulkLocked}
    {bulkTarget}
    canOn={actionableOn.length > 0}
    canStandby={actionableStandby.length > 0}
    canSleep={actionableSleep.length > 0}
    allOn={allStationsAtState('on')}
    allStandby={allStationsAtState('standby')}
    allSleep={allStationsAtState('sleep')}
    onCount={fleetOn}
    standbyCount={fleetStandby}
    sleepCount={fleetSleep}
    onScan={handleScanClick}
    onStop={handleStopScan}
    stopping={stoppingScan}
    onBulkPower={handleBulkPower}
  />

  <main>
    {#if conflictStations.length}
      <div class="alert danger" title={conflictDetails}><CircleAlert size={18} /> <span class="alert-text">Channel conflict: {conflictDetails}</span></div>
    {/if}
    {#if sortedStations.length}
      <ChannelMap stations={sortedStations} onSelect={(address) => selectedAddress = address} />
      <div class="station-grid">
        {#each sortedStations as station, index (station.address)}
          <div
            animate:flip={{ duration: 300, easing: cubicOut }}
            in:fade={{ duration: 180, delay: Math.min(index * 30, 240) }}
          >
            <StationCard
              {station}
              renaming={editingAddress === station.address}
              feedback={powerFeedbackByAddress[station.address]}
              pendingTarget={powerTargetByAddress[station.address]}
              gattBusy={gattOperations.has(station.address)}
              configBusy={configOperations.has(station.address)}
              gattLocked={stationLocked || (gattCapacityReached && !gattOperations.has(station.address))}
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
      <div class="empty" in:fade={{ duration: 180 }}>
        <div class="empty-icon"><Radar size={40} /></div>
        <p>{isLoading ? 'Scanning for base stations...' : 'External scan in progress...'} {Math.max(1, scanElapsed)}s</p>
        <p class="scan-sub">{scanElapsed >= 6 ? 'Reading station states...' : 'Discovering nearby stations...'}</p>
      </div>
    {:else}
      <div class="empty">
        <div class="empty-icon"><Activity size={40} /></div>
        <p>No base stations found.</p>
        <button class="btn primary" disabled={scanLocked} on:click={handleScanClick}>
          Scan Now
        </button>
      </div>
    {/if}
  </main>

  <StatusFooter {statusMessage} {apiRunning} {apiError} {configWarnings} {configWritable} />
</div>

{#if selectedStation}
  <div
    class="scrim"
    role="presentation"
    transition:fade={{ duration: 200 }}
    on:click={() => selectedAddress = null}
  ></div>
  <DetailsDrawer
    station={selectedStation}
    busy={stationBusy(selectedStation.address)}
    locked={stationLocked || (gattCapacityReached && !gattOperations.has(selectedStation.address))}
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
    transition:fade={{ duration: 180 }}
    on:click={closeChannelEditor}
  >
    <ChannelModal
      station={selectedStation}
      {occupiedChannels}
      {hasUnknownVisibleChannel}
      error={channelError}
      warning={channelWarning}
      busy={gattOperations.has(selectedStation.address) || configOperations.has(selectedStation.address)}
      locked={stationLocked || (gattCapacityReached && !gattOperations.has(selectedStation.address))}
      onClose={closeChannelEditor}
      onSave={saveChannel}
      onIdentify={identify}
    />
  </div>
{/if}

<Toast />

<style>
  .app-container { display: flex; flex-direction: column; height: 100vh; }
  main { flex: 1; padding: var(--spacing-md); overflow: auto; }
  .station-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: 0.75rem;
  }
  .scrim {
    position: fixed;
    inset: 0;
    background: rgba(3, 6, 12, 0.6);
    backdrop-filter: blur(2px);
    z-index: 10;
  }
  .modal-scrim {
    position: fixed;
    inset: 0;
    background: rgba(3, 6, 12, 0.6);
    backdrop-filter: blur(2px);
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
    width: 88px;
    height: 88px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius-pill);
    border: 1px solid var(--color-border);
    background: var(--bg-surface);
    color: var(--color-primary);
    box-shadow: 0 0 32px rgba(76, 141, 255, 0.15);
  }
  .empty-icon::after {
    content: '';
    position: absolute;
    inset: -1px;
    border-radius: var(--radius-pill);
    border: 1px solid color-mix(in srgb, var(--color-primary) 55%, transparent);
    animation: ping-ring 2.2s var(--ease) infinite;
  }
  @keyframes ping-ring {
    0% { transform: scale(0.9); opacity: 1; }
    70% { transform: scale(1.35); opacity: 0; }
    100% { transform: scale(1.35); opacity: 0; }
  }
  .empty p { margin: 0; font-size: 0.85rem; }
  .empty .scan-sub { font-size: var(--fs-sm); color: var(--text-muted); }
</style>
