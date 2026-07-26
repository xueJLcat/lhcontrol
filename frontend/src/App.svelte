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
    SetStationPower
  } from '../wailsjs/go/main/App';
  import { EventsOn } from '../wailsjs/runtime/runtime';
  import { Activity, CircleAlert } from 'lucide-svelte';
  import type { PowerFeedback, PowerTarget, StationInfo } from './lib/types';
  import {
    canSetPower, isCurrentPowerState, maySetPower, powerTargetLabel, stateLabel
  } from './lib/station';
  import { pushToast } from './lib/toast';
  import { deriveOperationLocks, type GlobalOperation } from './lib/operation-state';
  import { RevisionGate } from './lib/revision-gate';
  import AppHeader from './lib/components/AppHeader.svelte';
  import StationCard from './lib/components/StationCard.svelte';
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
  let apiRunning = false;
  let apiError = '';
  let externalScanning = false;
  const listRevisions = new RevisionGate();
  const apiRevisions = new RevisionGate();
  let disposed = false;

  $: sortedStations = [...stations].sort((a, b) => {
    const ac = a.channel > 0 ? a.channel : Number.MAX_SAFE_INTEGER;
    const bc = b.channel > 0 ? b.channel : Number.MAX_SAFE_INTEGER;
    return ac - bc || a.name.localeCompare(b.name) || a.address.localeCompare(b.address);
  });
  $: selectedStation = stations.find((station) => station.address === selectedAddress) ?? null;
  $: conflictStations = stations.filter((station) => station.channelConflict);
  $: occupiedChannels = new Map(
    stations
      .filter((station) => station.isPresent && station.scanFresh && station.channelFresh &&
        station.address !== selectedAddress && station.channel > 0)
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
  $: anyDeviceOperation = operationLocks.anyDeviceOperation;
  $: scanLocked = operationLocks.scanLocked;
  $: bulkLocked = operationLocks.bulkLocked;
  $: stationLocked = operationLocks.stationLocked;
  $: gattCapacityReached = gattOperations.size >= 2;

  function stationBusy(address: string): boolean {
    return gattOperations.has(address) || configOperations.has(address);
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

  function applyStationList(updated: StationInfo[] | null | undefined, revision: number): boolean {
    if (disposed || !listRevisions.isCurrent(revision)) return false;
    stations = updated || [];
    return true;
  }

  onMount(async () => {
    refreshAPIStatus();
    apiStatusInterval = setInterval(refreshAPIStatus, 15000);
    cancelExternalScanStartedListener = EventsOn('external-scan-started', () => {
      if (disposed) return;
      listRevisions.next();
      cancelRename();
      externalScanning = true;
      statusMessage = 'External scan in progress...';
    });
    cancelExternalScanListener = EventsOn('external-scan-completed', async (updated: StationInfo[]) => {
      if (disposed) return;
      const revision = listRevisions.next();
      externalScanning = false;
      stations = updated || [];
      const scanStatus = await GetScanStatus().catch(() => null);
      if (disposed || !listRevisions.isCurrent(revision)) return;
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = `External scan completed: found ${found}; ${stations.length} known station(s).`;
    });
    cancelExternalScanFailureListener = EventsOn('external-scan-failed', (message: string) => {
      if (disposed) return;
      listRevisions.next();
      externalScanning = false;
      statusMessage = `External scan failed: ${message}`;
      pushToast(`External scan failed: ${message}`);
    });
    statusCheckInterval = setInterval(periodicStatusCheck, 15000);
    const startupRevision = listRevisions.next();
    const startupScanning = await IsScanning().catch(() => false);
    if (disposed || !listRevisions.isCurrent(startupRevision)) return;
    externalScanning = startupScanning;
    if (externalScanning) {
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
    if (statusCheckInterval) clearInterval(statusCheckInterval);
    if (apiStatusInterval) clearInterval(apiStatusInterval);
    cancelExternalScanListener?.();
    cancelExternalScanFailureListener?.();
    cancelExternalScanStartedListener?.();
  });

  async function periodicStatusCheck() {
    if (externalScanning || isStatusChecking || isLoading || isBulkLoading || anyDeviceOperation) return;
    globalOperation = 'status-refresh';
    const revision = listRevisions.next();
    try {
      const scanning = await IsScanning();
      if (disposed || !listRevisions.isCurrent(revision)) return;
      externalScanning = scanning && !isLoading;
      if (!scanning) {
        if (!applyStationList(await CheckAllStationStatuses(), revision)) return;
      }
    } catch (error) {
      if (disposed || !listRevisions.isCurrent(revision)) return;
      console.error('Periodic status check failed:', error);
      const fallback = await GetCurrentStationInfo().catch(() => stations);
      if (!applyStationList(fallback, revision)) return;
      statusMessage = `Status refresh incomplete: ${error}`;
    } finally {
      if (!disposed && globalOperation === 'status-refresh') globalOperation = 'idle';
    }
  }

  async function handleScanClick() {
    if (isLoading || scanLocked) return;
    cancelRename();
    globalOperation = 'scanning';
    gattOperations = new Set();
    configOperations = new Set();
    powerTargetByAddress = {};
    powerFeedbackByAddress = {};
    const revision = listRevisions.next();
    statusMessage = 'Scanning for base stations...';
    try {
      if (!applyStationList(await ScanAndFetchStations(), revision)) return;
      const scanStatus = await GetScanStatus().catch(() => null);
      if (disposed || !listRevisions.isCurrent(revision)) return;
      const warning = scanStatus?.warnings?.join(' ');
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = warning
        ? `Found ${found}; ${stations.length} known station(s). ${warning}`
        : found ? `Found ${found}; ${stations.length} known station(s).` : 'No stations found in this scan.';
    } catch (error) {
      if (disposed || !listRevisions.isCurrent(revision)) return;
      statusMessage = `Scan failed: ${error}`;
      pushToast(`Scan failed: ${error}`);
    } finally {
      if (!disposed && globalOperation === 'scanning') globalOperation = 'idle';
    }
  }

  async function fetchLatestList(revision = listRevisions.next()): Promise<boolean> {
    try {
      return applyStationList(await GetCurrentStationInfo(), revision);
    } catch (error) {
      if (disposed || !listRevisions.isCurrent(revision)) return false;
      statusMessage = `Error refreshing list: ${error}`;
      return false;
    }
  }

  async function setPower(station: StationInfo, state: PowerTarget) {
    if (!canSetPower(station, state) || stationBusy(station.address) || gattLockedFor(station.address)) return;
    const targetLabel = powerTargetLabel(state);
    setGattBusy(station.address, true);
    powerTargetByAddress = { ...powerTargetByAddress, [station.address]: state };
    powerFeedbackByAddress = {
      ...powerFeedbackByAddress,
      [station.address]: { kind: 'pending', text: `Switching to ${targetLabel}…` }
    };
    statusMessage = `Setting ${station.name} to ${targetLabel}…`;
    try {
      const result = await SetStationPower(station.address, state);
      if (disposed) return;
      const revision = listRevisions.next();
      if (!listRevisions.isCurrent(revision)) return;
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
      if (disposed) return;
      const errorText = String(error);
      if (!await fetchLatestList()) return;
      const actual = stations.find((current) => current.address === station.address);
      const actualState = actual ? stateLabel(actual) : 'unknown';
      powerFeedbackByAddress = {
        ...powerFeedbackByAddress,
        [station.address]: { kind: 'error', text: `Failed · actual ${actualState}` }
      };
      statusMessage = `Power change failed for ${station.name}: ${errorText}`;
      pushToast(`Power change failed for ${station.name}: ${errorText}`);
    } finally {
      if (!disposed) {
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
    if (bulkLocked || stations.length === 0) return;
    globalOperation = 'bulk-power';
    bulkTarget = state;
    const targetLabel = powerTargetLabel(state);
    statusMessage = `Setting all available stations to ${targetLabel}…`;
    try {
      const result = await SetAllStationsPowerDetailed(state);
      if (disposed) return;
      if (!await fetchLatestList()) return;
      const feedback = { ...powerFeedbackByAddress };
      for (const item of result.results) {
        feedback[item.address] = item.skipped
          ? item.success && item.confirmed
            ? { kind: 'success', text: `Already ${targetLabel}` }
            : { kind: 'warning', text: `Skipped · ${item.reason || 'not actionable'}` }
          : item.success && item.confirmed
            ? { kind: 'success', text: `${targetLabel} confirmed` }
            : item.success && item.commandSent
              ? { kind: 'warning', text: `${targetLabel} sent · status unavailable` }
              : { kind: 'error', text: item.error || `Failed to set ${targetLabel}` };
      }
      powerFeedbackByAddress = feedback;
      const confirmed = result.results.filter((item) => item.success && !item.skipped && item.confirmed).length;
      const unconfirmed = result.results.filter((item) => item.success && !item.skipped && !item.confirmed).length;
      const skipped = result.results.filter((item) => item.skipped);
      const failed = result.results.filter((item) => !item.success && !item.skipped && !item.commandSent);
      statusMessage = failed.length
        ? `${confirmed} confirmed, ${unconfirmed} sent but unconfirmed, ${skipped.length} skipped, ${failed.length} failed: ${failed.map((item) => `${item.name || item.address}: ${item.error}`).join(' | ')}`
        : `${confirmed} confirmed; ${unconfirmed} sent but unconfirmed; ${skipped.length} skipped for ${targetLabel}.`;
      if (failed.length) pushToast(`Bulk ${targetLabel}: ${failed.length} station(s) failed. See status bar.`);
    } catch (error) {
      if (disposed) return;
      if (!await fetchLatestList()) return;
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
    try {
      await RenameStationByAddress(station.address, name);
      if (disposed) return;
      if (!await fetchLatestList()) return;
      statusMessage = name ? `Renamed to ${name}.` : `Reset name for ${station.originalName}.`;
    } catch (error) {
      if (disposed) return;
      statusMessage = `Error renaming: ${error}`;
      pushToast(`Error renaming: ${error}`);
    } finally {
      if (!disposed) setConfigBusy(station.address, false);
    }
  }

  async function identify(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    try {
      await IdentifyStation(station.address);
      if (disposed) return;
      if (!await fetchLatestList()) return;
      statusMessage = `Identify signal sent to ${station.name}.`;
    } catch (error) {
      if (disposed) return;
      if (!await fetchLatestList()) return;
      statusMessage = `Identify failed for ${station.name}: ${error}`;
      pushToast(`Identify failed for ${station.name}: ${error}`);
    } finally {
      if (!disposed) setGattBusy(station.address, false);
    }
  }

  async function refreshCapabilities(station: StationInfo) {
    if (stationBusy(station.address) || gattLockedFor(station.address)) return;
    setGattBusy(station.address, true);
    try {
      const updated = await RefreshStationCapabilities(station.address);
      if (disposed) return;
      const revision = listRevisions.next();
      if (!listRevisions.isCurrent(revision)) return;
      stations = stations.map((current) => current.address === station.address ? updated : current);
      statusMessage = updated.lastError
        ? `Capabilities refreshed for ${station.name}, but some values are unavailable: ${updated.lastError}`
        : `Capabilities refreshed for ${station.name}.`;
    } catch (error) {
      if (disposed) return;
      if (!await fetchLatestList()) return;
      statusMessage = `Capability refresh failed for ${station.name}: ${error}`;
      pushToast(`Capability refresh failed for ${station.name}: ${error}`);
    } finally {
      if (!disposed) setGattBusy(station.address, false);
    }
  }

  function openChannelEditor(_station: StationInfo) {
    channelError = '';
    channelEditorOpen = true;
  }

  async function saveChannel(targetChannel: number, allowUnknownConflictRisk: boolean) {
    if (!selectedStation || !selectedStation.scanFresh ||
      stationBusy(selectedStation.address) || gattLockedFor(selectedStation.address) ||
      (selectedStation.channel > 0 && selectedStation.channel === targetChannel)) return;
    const address = selectedStation.address;
    setGattBusy(address, true);
    channelError = '';
    try {
      const result = await SetStationChannel(address, targetChannel, allowUnknownConflictRisk);
      if (disposed) return;
      if (!await fetchLatestList()) return;
      channelEditorOpen = false;
      statusMessage = `Channel changed from ${result.previousChannel || 'unknown'} to ${result.channel}. ${result.warnings.join(' ')}`;
    } catch (error) {
      if (disposed) return;
      if (!await fetchLatestList()) return;
      const actual = stations.find((station) => station.address === address)?.channel ?? 0;
      channelError = `${String(error)} Actual readback: ${actual || 'unknown'}.`;
    } finally {
      if (!disposed) setGattBusy(address, false);
    }
  }

  function handleGlobalKeydown(event: KeyboardEvent) {
    if (event.key !== 'Escape') return;
    if (channelEditorOpen) {
      channelEditorOpen = false;
    } else if (selectedAddress) {
      selectedAddress = null;
    }
  }
</script>

<svelte:window on:keydown={handleGlobalKeydown} />

<div class="app-container">
  <AppHeader
    {isLoading}
    {isBulkLoading}
    {scanLocked}
    {bulkLocked}
    {bulkTarget}
    canOn={stations.length > 0}
    canStandby={stations.length > 0}
    canSleep={stations.length > 0}
    allOn={allEligibleAtState('on')}
    allStandby={allEligibleAtState('standby')}
    allSleep={allEligibleAtState('sleep')}
    onScan={handleScanClick}
    onBulkPower={handleBulkPower}
  />

  <main>
    {#if conflictStations.length}
      <div class="alert danger"><CircleAlert size={18} /> Channel conflict detected on {conflictStations.length} visible station(s).</div>
    {/if}
    {#if sortedStations.length}
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
    {:else if !isLoading}
      <div class="empty">
        <div class="empty-icon"><Activity size={40} /></div>
        <p>No base stations found.</p>
        <button class="btn primary" on:click={handleScanClick}>Scan Now</button>
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
    on:click={() => channelEditorOpen = false}
  >
    <ChannelModal
      station={selectedStation}
      {occupiedChannels}
      {hasUnknownVisibleChannel}
      error={channelError}
      busy={stationBusy(selectedStation.address)}
      locked={stationLocked || (gattOperations.size >= 2 && !gattOperations.has(selectedStation.address))}
      onClose={() => channelEditorOpen = false}
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
    grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
    gap: 0.6rem;
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
    width: 88px;
    height: 88px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 999px;
    border: 1px solid var(--color-border);
    background: var(--bg-surface);
    color: var(--color-primary);
    box-shadow: 0 0 32px rgba(76, 141, 255, 0.15);
  }
  .empty p { margin: 0; font-size: 0.85rem; }
</style>
