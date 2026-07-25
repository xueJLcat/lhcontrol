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
  import AppHeader from './lib/components/AppHeader.svelte';
  import StationCard from './lib/components/StationCard.svelte';
  import DetailsDrawer from './lib/components/DetailsDrawer.svelte';
  import ChannelModal from './lib/components/ChannelModal.svelte';
  import StatusFooter from './lib/components/StatusFooter.svelte';
  import Toast from './lib/components/Toast.svelte';

  let stations: StationInfo[] = [];
  let statusMessage = 'Ready to scan.';
  let operationInProgress: Record<string, boolean> = {};
  let powerTargetByAddress: Record<string, PowerTarget | undefined> = {};
  let powerFeedbackByAddress: Record<string, PowerFeedback | undefined> = {};
  let isLoading = false;
  let isBulkLoading = false;
  let isStatusChecking = false;
  let bulkTarget: PowerTarget | null = null;
  let editingAddress: string | null = null;
  let selectedAddress: string | null = null;
  let channelEditorOpen = false;
  let channelError = '';
  let statusCheckInterval: ReturnType<typeof setInterval> | null = null;
  let apiStatusInterval: ReturnType<typeof setInterval> | null = null;
  let cancelExternalScanListener: (() => void) | null = null;
  let cancelExternalScanFailureListener: (() => void) | null = null;
  let apiRunning = false;
  let apiError = '';

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
  $: anyDeviceOperation = Object.values(operationInProgress).some(Boolean) ||
    Object.values(powerTargetByAddress).some(Boolean);
  $: bluetoothControlBusy = anyDeviceOperation;
  $: globalLocked = isLoading || isBulkLoading;

  function stationBusy(address: string): boolean {
    return Boolean(operationInProgress[address] || powerTargetByAddress[address]);
  }

  onMount(() => {
    refreshAPIStatus();
    apiStatusInterval = setInterval(refreshAPIStatus, 15000);
    cancelExternalScanListener = EventsOn('external-scan-completed', async (updated: StationInfo[]) => {
      stations = updated || [];
      const scanStatus = await GetScanStatus().catch(() => null);
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = `External scan completed: found ${found}; ${stations.length} known station(s).`;
    });
    cancelExternalScanFailureListener = EventsOn('external-scan-failed', (message: string) => {
      statusMessage = `External scan failed: ${message}`;
      pushToast(`External scan failed: ${message}`);
    });
    statusCheckInterval = setInterval(periodicStatusCheck, 15000);
    handleScanClick();
  });

  function refreshAPIStatus() {
    GetAPIStatus().then((status) => {
      apiRunning = status.running;
      apiError = status.error;
    }).catch((error) => {
      apiRunning = false;
      apiError = String(error);
    });
  }

  onDestroy(() => {
    if (statusCheckInterval) clearInterval(statusCheckInterval);
    if (apiStatusInterval) clearInterval(apiStatusInterval);
    cancelExternalScanListener?.();
    cancelExternalScanFailureListener?.();
  });

  async function periodicStatusCheck() {
    if (isStatusChecking || isLoading || isBulkLoading || anyDeviceOperation) return;
    isStatusChecking = true;
    try {
      if (!(await IsScanning())) {
        stations = (await CheckAllStationStatuses()) || [];
      }
    } catch (error) {
      console.error('Periodic status check failed:', error);
      stations = (await GetCurrentStationInfo().catch(() => stations)) || stations;
      statusMessage = `Status refresh incomplete: ${error}`;
    } finally {
      isStatusChecking = false;
    }
  }

  async function handleScanClick() {
    if (isLoading || isBulkLoading || isStatusChecking || bluetoothControlBusy) return;
    isLoading = true;
    operationInProgress = {};
    powerTargetByAddress = {};
    powerFeedbackByAddress = {};
    statusMessage = 'Scanning for base stations...';
    try {
      stations = (await ScanAndFetchStations()) || [];
      const scanStatus = await GetScanStatus().catch(() => null);
      const warning = scanStatus?.warnings?.join(' ');
      const found = scanStatus?.found ?? stations.filter((station) => station.seenInLatestScan).length;
      statusMessage = warning
        ? `Found ${found}; ${stations.length} known station(s). ${warning}`
        : found ? `Found ${found}; ${stations.length} known station(s).` : 'No stations found in this scan.';
    } catch (error) {
      statusMessage = `Scan failed: ${error}`;
      pushToast(`Scan failed: ${error}`);
    } finally {
      isLoading = false;
    }
  }

  async function fetchLatestList() {
    cancelRename();
    try {
      stations = (await GetCurrentStationInfo()) || [];
    } catch (error) {
      statusMessage = `Error refreshing list: ${error}`;
    }
  }

  async function setPower(station: StationInfo, state: PowerTarget) {
    if (!canSetPower(station, state) || stationBusy(station.address) || globalLocked) return;
    const targetLabel = powerTargetLabel(state);
    powerTargetByAddress = { ...powerTargetByAddress, [station.address]: state };
    powerFeedbackByAddress = {
      ...powerFeedbackByAddress,
      [station.address]: { kind: 'pending', text: `Switching to ${targetLabel}…` }
    };
    statusMessage = `Setting ${station.name} to ${targetLabel}…`;
    try {
      const result = await SetStationPower(station.address, state);
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
      const errorText = String(error);
      await fetchLatestList();
      const actual = stations.find((current) => current.address === station.address);
      const actualState = actual ? stateLabel(actual) : 'unknown';
      powerFeedbackByAddress = {
        ...powerFeedbackByAddress,
        [station.address]: { kind: 'error', text: `Failed · actual ${actualState}` }
      };
      statusMessage = `Power change failed for ${station.name}: ${errorText}`;
      pushToast(`Power change failed for ${station.name}: ${errorText}`);
    } finally {
      powerTargetByAddress = { ...powerTargetByAddress, [station.address]: undefined };
    }
  }

  function eligiblePowerStations(state: PowerTarget): StationInfo[] {
    return stations.filter((station) =>
      station.isPresent && maySetPower(station, state)
    );
  }

  function hasEligiblePowerStations(state: PowerTarget): boolean {
    return eligiblePowerStations(state).some((station) => !isCurrentPowerState(station, state));
  }

  function allEligibleAtState(state: PowerTarget): boolean {
    const eligible = eligiblePowerStations(state);
    return eligible.length > 0 && eligible.every((station) => isCurrentPowerState(station, state));
  }

  async function handleBulkPower(state: PowerTarget) {
    if (isLoading || isBulkLoading || stations.length === 0) return;
    isBulkLoading = true;
    bulkTarget = state;
    const targetLabel = powerTargetLabel(state);
    statusMessage = `Setting all available stations to ${targetLabel}…`;
    try {
      const result = await SetAllStationsPowerDetailed(state);
      await fetchLatestList();
      const confirmed = result.results.filter((item) => item.success && !item.skipped && item.confirmed).length;
      const unconfirmed = result.results.filter((item) => item.success && !item.skipped && !item.confirmed).length;
      const skipped = result.results.filter((item) => item.skipped);
      const failed = result.results.filter((item) => !item.success && !item.skipped);
      statusMessage = failed.length
        ? `${confirmed} confirmed, ${unconfirmed} sent but unconfirmed, ${skipped.length} skipped, ${failed.length} failed: ${failed.map((item) => `${item.name || item.address}: ${item.error}`).join(' | ')}`
        : `${confirmed} confirmed; ${unconfirmed} sent but unconfirmed; ${skipped.length} skipped for ${targetLabel}.`;
      if (failed.length) pushToast(`Bulk ${targetLabel}: ${failed.length} station(s) failed. See status bar.`);
    } catch (error) {
      await fetchLatestList();
      statusMessage = `Bulk ${targetLabel} operation partially failed: ${error}`;
      pushToast(`Bulk ${targetLabel} operation partially failed: ${error}`);
    } finally {
      isBulkLoading = false;
      bulkTarget = null;
    }
  }

  function startRename(station: StationInfo) {
    if (operationInProgress[station.address]) return;
    editingAddress = station.address;
  }

  function cancelRename() {
    editingAddress = null;
  }

  async function saveRename(station: StationInfo, name: string) {
    cancelRename();
    if (name === station.name) return;
    try {
      await RenameStationByAddress(station.address, name);
      await fetchLatestList();
      statusMessage = name ? `Renamed to ${name}.` : `Reset name for ${station.originalName}.`;
    } catch (error) {
      statusMessage = `Error renaming: ${error}`;
      pushToast(`Error renaming: ${error}`);
    }
  }

  async function identify(station: StationInfo) {
    if (stationBusy(station.address) || globalLocked) return;
    operationInProgress = { ...operationInProgress, [station.address]: true };
    try {
      await IdentifyStation(station.address);
      await fetchLatestList();
      statusMessage = `Identify signal sent to ${station.name}.`;
    } catch (error) {
      await fetchLatestList();
      statusMessage = `Identify failed for ${station.name}: ${error}`;
      pushToast(`Identify failed for ${station.name}: ${error}`);
    } finally {
      operationInProgress = { ...operationInProgress, [station.address]: false };
    }
  }

  async function refreshCapabilities(station: StationInfo) {
    if (stationBusy(station.address) || globalLocked) return;
    operationInProgress = { ...operationInProgress, [station.address]: true };
    try {
      const updated = await RefreshStationCapabilities(station.address);
      stations = stations.map((current) => current.address === station.address ? updated : current);
      statusMessage = `Capabilities refreshed for ${station.name}.`;
    } catch (error) {
      await fetchLatestList();
      statusMessage = `Capability refresh failed for ${station.name}: ${error}`;
      pushToast(`Capability refresh failed for ${station.name}: ${error}`);
    } finally {
      operationInProgress = { ...operationInProgress, [station.address]: false };
    }
  }

  function openChannelEditor(_station: StationInfo) {
    channelError = '';
    channelEditorOpen = true;
  }

  async function saveChannel(targetChannel: number) {
    if (!selectedStation || !selectedStation.scanFresh ||
      stationBusy(selectedStation.address) || globalLocked ||
      (selectedStation.channel > 0 && selectedStation.channel === targetChannel)) return;
    const address = selectedStation.address;
    operationInProgress = { ...operationInProgress, [address]: true };
    channelError = '';
    try {
      const result = await SetStationChannel(address, targetChannel);
      await fetchLatestList();
      channelEditorOpen = false;
      statusMessage = `Channel changed from ${result.previousChannel || 'unknown'} to ${result.channel}. ${result.warnings.join(' ')}`;
    } catch (error) {
      await fetchLatestList();
      const actual = stations.find((station) => station.address === address)?.channel ?? 0;
      channelError = `${String(error)} Actual readback: ${actual || 'unknown'}.`;
    } finally {
      operationInProgress = { ...operationInProgress, [address]: false };
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
    locked={isLoading || isBulkLoading}
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
              busy={stationBusy(station.address)}
              locked={globalLocked}
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
    locked={globalLocked}
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
      locked={globalLocked}
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
