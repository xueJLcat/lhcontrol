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
    canSetPower, hasCurrentChannel, hasVerifiedPowerState, isCurrentPowerState, maySetPower,
    powerTargetLabel, stateLabel
  } from './lib/station';
  import { formatBulkResult, formatScanResult, summarizeBulkResult } from './lib/result-format';
  import { pushToast } from './lib/toast';
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
  let statusCheckInterval: ReturnType<typeof setInterval> | null = null;
  let apiStatusInterval: ReturnType<typeof setInterval> | null = null;
  let cancelExternalScanListener: (() => void) | null = null;
  let cancelExternalScanFailureListener: (() => void) | null = null;
  let cancelExternalScanStartedListener: (() => void) | null = null;
  let cancelExternalScanCancelledListener: (() => void) | null = null;
  let apiRunning = false;
  let apiError = '';
  let externalScanning = false;
  let stoppingScan = false;
  let scanStartedAt: number | null = null;
  let scanElapsed = 0;
  let scanTimer: ReturnType<typeof setInterval> | null = null;
  const listRevisions = new RevisionGate();
  const apiRevisions = new RevisionGate();
  let scanEpoch = 0;
  let nextStationRevision = 0;
  let stationRevisions = new Map<string, number>();
  let disposed = false;

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
  $: eligibleOn = stations.filter((station) => maySetPower(station, 'on'));
  $: eligibleStandby = stations.filter((station) => maySetPower(station, 'standby'));
  $: eligibleSleep = stations.filter((station) => maySetPower(station, 'sleep'));
  $: occupiedChannels = new Map(
    stations
      .filter((station) => hasCurrentChannel(station) && station.address !== selectedAddress)
      .map((station) => [station.channel, station.name])
  );
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
  $: scanningActive = isLoading || externalScanning;
  $: if (!disposed) {
    // The lighthouse beam is a scanning indicator: it only sweeps while a
    // scan is active, so its motion always means "scan in progress".
    document.body.classList.toggle('scanning', scanningActive);
  }
  $: anyDeviceOperation = operationLocks.anyDeviceOperation;
  $: scanLocked = operationLocks.scanLocked;
  $: bulkLocked = operationLocks.bulkLocked;
  $: stationLocked = operationLocks.stationLocked;
  $: gattCapacityReached = gattOperations.size >= 2;

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

  function prepareForScan() {
    cancelRename();
    channelEditorOpen = false;
    channelError = '';
    // A started scan has already acquired the backend's exclusive operation
    // lock, so any older device/config request has finished server-side even
    // if its Wails promise has not settled in this renderer yet.
    gattOperations = new Set();
    configOperations = new Set();
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
    refreshAPIStatus();
    apiStatusInterval = setInterval(refreshAPIStatus, 15000);
    cancelExternalScanStartedListener = EventsOn('external-scan-started', () => {
      if (disposed) return;
      beginScanEpoch();
      listRevisions.next();
      prepareForScan();
      externalScanning = true;
      stoppingScan = false;
      beginScanTimer();
      statusMessage = 'External scan in progress...';
    });
    cancelExternalScanListener = EventsOn('external-scan-completed', async (updated: StationInfo[]) => {
      if (disposed) return;
      beginScanEpoch();
      const revision = listRevisions.next();
      prepareForScan();
      externalScanning = false;
      stoppingScan = false;
      maybeEndScanTimer();
      stations = updated || [];
      const scanStatus = await GetScanStatus().catch(() => null);
      if (disposed || !listRevisions.isCurrent(revision)) return;
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = formatScanResult({ found, warnings: scanStatus?.warnings }, stations.length, true);
    });
    cancelExternalScanFailureListener = EventsOn('external-scan-failed', async (message: string) => {
      if (disposed) return;
      const operationEpoch = beginScanEpoch();
      const revision = listRevisions.next();
      prepareForScan();
      externalScanning = false;
      stoppingScan = false;
      maybeEndScanTimer();
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (updated) applyStationList(updated, revision);
      statusMessage = `External scan failed: ${message}`;
      pushToast(`External scan failed: ${message}`);
    });
    cancelExternalScanCancelledListener = EventsOn('external-scan-cancelled', async () => {
      if (disposed) return;
      const operationEpoch = beginScanEpoch();
      const revision = listRevisions.next();
      prepareForScan();
      externalScanning = false;
      stoppingScan = false;
      maybeEndScanTimer();
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (updated) applyStationList(updated, revision);
      statusMessage = 'Scan stopped.';
    });
    statusCheckInterval = setInterval(periodicStatusCheck, 15000);
    const startupRevision = listRevisions.next();
    const startupScanning = await IsScanning().catch(() => false);
    if (disposed || !listRevisions.isCurrent(startupRevision)) return;
    externalScanning = startupScanning;
    if (externalScanning) {
      beginScanTimer();
      statusMessage = 'External scan in progress...';
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
    }).catch((error) => {
      if (disposed || !apiRevisions.isCurrent(revision)) return;
      apiRunning = false;
      apiError = String(error);
    });
  }

  onDestroy(() => {
    disposed = true;
    listRevisions.dispose();
    apiRevisions.dispose();
    endScanTimer();
    document.body.classList.remove('scanning');
    if (statusCheckInterval) clearInterval(statusCheckInterval);
    if (apiStatusInterval) clearInterval(apiStatusInterval);
    cancelExternalScanListener?.();
    cancelExternalScanFailureListener?.();
    cancelExternalScanStartedListener?.();
    cancelExternalScanCancelledListener?.();
  });

  async function periodicStatusCheck() {
    if (isStatusChecking || isLoading || isBulkLoading || anyDeviceOperation) return;
    globalOperation = 'status-refresh';
    const revision = listRevisions.next();
    const capturedStationRevisions = new Map(stationRevisions);
    try {
      const scanning = await IsScanning();
      if (disposed || !listRevisions.isCurrent(revision)) return;
      const wasExternalScanning = externalScanning;
      externalScanning = scanning && !isLoading;
      if (!scanning) {
        stoppingScan = false;
        if (wasExternalScanning) {
          if (!applyStationList(await GetCurrentStationInfo(), revision, capturedStationRevisions)) return;
          const scanStatus = await GetScanStatus().catch(() => null);
          if (disposed || !listRevisions.isCurrent(revision)) return;
          const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
          statusMessage = scanStatus?.state === 'cancelled'
            ? 'Scan stopped.'
            : formatScanResult({ found, warnings: scanStatus?.warnings }, stations.length, true);
        } else if (!applyStationList(await CheckAllStationStatuses(), revision, capturedStationRevisions)) return;
        maybeEndScanTimer();
      } else {
        beginScanTimer();
      }
    } catch (error) {
      if (disposed || !listRevisions.isCurrent(revision)) return;
      console.error('Periodic status check failed:', error);
      const fallback = await GetCurrentStationInfo().catch(() => stations);
      if (!applyStationList(fallback, revision, capturedStationRevisions)) return;
      statusMessage = `Status refresh incomplete: ${error}`;
    } finally {
      if (!disposed && globalOperation === 'status-refresh') globalOperation = 'idle';
    }
  }

  async function handleScanClick() {
    if (isLoading || scanLocked) return;
    prepareForScan();
    globalOperation = 'scanning';
    beginScanTimer();
    const operationEpoch = beginScanEpoch();
    const revision = listRevisions.next();
    statusMessage = 'Scanning for base stations...';
    try {
      if (!applyStationList(await ScanAndFetchStations(), revision)) return;
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = scanStatus?.state === 'cancelled'
        ? 'Scan stopped.'
        : formatScanResult({ found, warnings: scanStatus?.warnings }, stations.length);
    } catch (error) {
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      const updated = await GetCurrentStationInfo().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (updated) applyStationList(updated, revision);
      const scanStatus = await GetScanStatus().catch(() => null);
      if (!canCommitOperation(operationEpoch) || !listRevisions.isCurrent(revision)) return;
      if (stoppingScan || scanStatus?.state === 'cancelled') {
        stoppingScan = false;
        statusMessage = 'Scan stopped.';
      } else {
        statusMessage = `Scan failed: ${error}`;
        pushToast(`Scan failed: ${error}`);
      }
    } finally {
      if (!disposed && globalOperation === 'scanning') globalOperation = 'idle';
      maybeEndScanTimer();
    }
  }

  async function handleStopScan() {
    if (!scanningActive || stoppingScan) return;
    stoppingScan = true;
    statusMessage = 'Stopping scan...';
    try {
      await StopScan();
      // A terminal event may have completed the UI transition while the
      // backend completion barrier was resolving.
      if (!stoppingScan) return;
      stoppingScan = false;
      externalScanning = false;
      statusMessage = 'Scan stopped.';
      maybeEndScanTimer();
    } catch (error) {
      stoppingScan = false;
      statusMessage = `Unable to stop scan: ${error}`;
      pushToast(statusMessage);
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
    return stations.find((current) => current.address === address) ?? station;
  }

  async function setPower(station: StationInfo, state: PowerTarget) {
    if (!canSetPower(station, state) || stationBusy(station.address) || gattLockedFor(station.address)) return;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = scanEpoch;
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
      statusMessage = result.confirmed
        ? `${station.name} is ${targetLabel}.`
        : result.confirmationError
          ? `${station.name}: command sent, but confirmation failed. ${result.confirmationError}`
          : `${station.name}: ${targetLabel} command sent; this firmware cannot confirm the state.`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      const errorText = String(error);
      const actual = await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      const actualState = actual ? stateLabel(actual) : 'unknown';
      powerFeedbackByAddress = {
        ...powerFeedbackByAddress,
        [station.address]: { kind: 'error', text: `Failed · actual ${actualState}` }
      };
      statusMessage = `Power change failed for ${station.name}: ${errorText}`;
      pushToast(`Power change failed for ${station.name}: ${errorText}`);
    } finally {
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
        powerTargetByAddress = { ...powerTargetByAddress, [station.address]: undefined };
        setGattBusy(station.address, false);
      }
    }
  }

  function eligiblePowerStations(state: PowerTarget): StationInfo[] {
    // A Lighthouse often stops advertising while connected or sleeping. Keep
    // bulk power aligned with individual controls by including every station
    // discovered during this app session, even if the latest scan missed it.
    return stations.filter((station) => maySetPower(station, state));
  }

  function allEligibleAtState(state: PowerTarget): boolean {
    const eligible = eligiblePowerStations(state);
    return eligible.length > 0 && eligible.every((station) => isCurrentPowerState(station, state));
  }

  async function handleBulkPower(state: PowerTarget) {
    // Do not duplicate backend capability/state decisions here. Cached
    // frontend data can be stale after scanning, while the backend refreshes
    // capabilities and returns a result for every known station.
    if (bulkLocked || eligiblePowerStations(state).length === 0) return;
    globalOperation = 'bulk-power';
    bulkTarget = state;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = scanEpoch;
    statusMessage = `Setting all available stations to ${targetLabel}…`;
    try {
      const result = await SetAllStationsPowerDetailed(state);
      if (!canCommitOperation(operationEpoch)) return;
      await fetchLatestList();
      if (!canCommitOperation(operationEpoch)) return;
      mergeStationUpdates(result.results.map((item) => item.station).filter(Boolean));
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
      statusMessage = formatBulkResult(targetLabel, summary);
      const toastKind = summary.failed.length ? 'error'
        : summary.unconfirmed || summary.skipped ? 'warning'
          : 'success';
      pushToast(`Bulk ${targetLabel}: ${formatBulkResult(targetLabel, summary)}`, toastKind);
    } catch (error) {
      if (!canCommitOperation(operationEpoch)) return;
      await fetchLatestList();
      if (!canCommitOperation(operationEpoch)) return;
      statusMessage = `Bulk ${targetLabel} operation partially failed: ${error}`;
      pushToast(`Bulk ${targetLabel} operation partially failed: ${error}`);
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
      cancelRename();
      return;
    }
    cancelRename();
    if (name === station.name) return;
    setConfigBusy(station.address, true);
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
      statusMessage = name ? `Renamed to ${name}.` : `Reset name for ${station.originalName}.`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      statusMessage = `Error renaming: ${error}`;
      pushToast(`Error renaming: ${error}`);
    } finally {
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
        setConfigBusy(station.address, false);
      }
    }
  }

  async function identify(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    const operationEpoch = scanEpoch;
    const operationRevision = beginStationOperationRevision(station.address);
    try {
      await IdentifyStation(station.address);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      statusMessage = `Identify signal sent to ${station.name}.`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      statusMessage = `Identify failed for ${station.name}: ${error}`;
      pushToast(`Identify failed for ${station.name}: ${error}`);
    } finally {
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
        setGattBusy(station.address, false);
      }
    }
  }

  async function refreshCapabilities(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    const operationEpoch = scanEpoch;
    const operationRevision = beginStationOperationRevision(station.address);
    try {
      const updated = await RefreshStationCapabilities(station.address);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      stations = stations.map((current) => current.address === station.address ? updated : current);
      statusMessage = updated.lastError
        ? `Capabilities refreshed for ${station.name}, but some values are unavailable: ${updated.lastError}`
        : `Capabilities refreshed for ${station.name}.`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      statusMessage = `Capability refresh failed for ${station.name}: ${error}`;
      pushToast(`Capability refresh failed for ${station.name}: ${error}`);
    } finally {
      if (canCommitStationOperation(operationEpoch, station.address, operationRevision)) {
        setGattBusy(station.address, false);
      }
    }
  }

  function openChannelEditor(_station: StationInfo) {
    channelError = '';
    channelEditorOpen = true;
  }

  function closeChannelEditor() {
    if (selectedStation && stationBusy(selectedStation.address)) return;
    channelEditorOpen = false;
  }

  async function saveChannel(targetChannel: number, allowUnknownConflictRisk: boolean) {
    if (!selectedStation || !selectedStation.scanFresh ||
      stationBusy(selectedStation.address) || gattLockedFor(selectedStation.address) ||
      (selectedStation.channel > 0 && selectedStation.channel === targetChannel)) return;
    const address = selectedStation.address;
    const operationEpoch = scanEpoch;
    const operationRevision = beginStationOperationRevision(address);
    setGattBusy(address, true);
    channelError = '';
    try {
      const result = await SetStationChannel(address, targetChannel, allowUnknownConflictRisk);
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      await fetchStationUpdate(address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      stations = stations.map((station) => station.address === address
        ? withStationChanges(station, { channel: result.channel, channelFresh: true, lastError: '' })
        : station);
      channelEditorOpen = false;
      statusMessage = `Channel changed from ${result.previousChannel || 'unknown'} to ${result.channel}. ${result.warnings.join(' ')}`;
    } catch (error) {
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      await fetchStationUpdate(address, operationEpoch, operationRevision);
      if (!canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      const actual = stations.find((station) => station.address === address)?.channel ?? 0;
      channelError = `${String(error)} Actual readback: ${actual || 'unknown'}.`;
      statusMessage = `Channel change failed: ${channelError}`;
      pushToast(statusMessage);
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
    scanning={isLoading || externalScanning}
    {isBulkLoading}
    {scanLocked}
    {bulkLocked}
    {bulkTarget}
    canOn={eligibleOn.length > 0}
    canStandby={eligibleStandby.length > 0}
    canSleep={eligibleSleep.length > 0}
    allOn={allEligibleAtState('on')}
    allStandby={allEligibleAtState('standby')}
    allSleep={allEligibleAtState('sleep')}
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
              gattLocked={stationLocked || (gattOperations.size >= 2 && !gattOperations.has(station.address))}
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

  <StatusFooter {statusMessage} {apiRunning} {apiError} />
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
    locked={stationLocked || (gattOperations.size >= 2 && !gattOperations.has(selectedStation.address))}
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
      busy={gattOperations.has(selectedStation.address) || configOperations.has(selectedStation.address)}
      locked={stationLocked || (gattOperations.size >= 2 && !gattOperations.has(selectedStation.address))}
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
