<script lang="ts">
  import { onDestroy, onMount, untrack } from 'svelte';
  import { fade } from 'svelte/transition';
  import type { PowerTarget } from './lib/types';
  import { clearToasts } from './lib/toast';
  import { dur } from './lib/motion';
  import { StationStore } from './lib/state/station-store.svelte.ts';
  import AppHeader from './lib/components/AppHeader.svelte';
  import FleetView from './lib/components/FleetView.svelte';
  import DetailsDrawer from './lib/components/DetailsDrawer.svelte';
  import SettingsPanel from './lib/components/SettingsPanel.svelte';
  import ChannelModal from './lib/components/ChannelModal.svelte';
  import StatusFooter from './lib/components/StatusFooter.svelte';
  import Toast from './lib/components/Toast.svelte';
  import BulkConfirmModal from './lib/components/BulkConfirmModal.svelte';

  // Overlay switches only; every fleet/backend concern lives in the store.
  let selectedAddress = $state<string | null>(null);
  let settingsOpen = $state(false);
  let channelEditorOpen = $state(false);
  let bulkConfirmTarget = $state<PowerTarget | null>(null);

  const store = new StationStore({
    closeChannelEditor: () => {
      closeChannelEditor();
    },
    forceCloseChannelEditor: () => {
      channelEditorOpen = false;
    },
    clearBulkConfirmation: () => {
      bulkConfirmTarget = null;
    },
    requestBulkConfirmation: (target) => {
      bulkConfirmTarget = target;
    }
  });

  const selectedStation = $derived(store.stations.find((station) => station.address === selectedAddress) ?? null);
  const occupiedChannels = $derived(store.occupiedChannelsExcluding(selectedAddress));
  const hasUnknownVisibleChannel = $derived(store.hasUnknownVisibleChannelExcluding(selectedAddress));

  // Keep the channel memory current before any derived list recomputes. The
  // pre effect runs before the render flush so the cache is fresh when sort
  // keys and child props are evaluated.
  $effect.pre(() => {
    store.syncChannelMemory();
  });

  // A station can drop out of the list while its drawer is open (backend
  // list replacement). Clear the stale selection so the drawer does not
  // silently reopen if the same address reappears later, and drop the
  // channel-editor state with it: leaving channelEditorOpen true would make
  // the next selected station open an inert drawer plus a channel modal that
  // belongs to the previous selection. Only the station list drives this
  // guard; the addresses are read untracked to avoid a reactive cycle with
  // selectedStation.
  $effect(() => {
    const list = store.stations;
    const address = untrack(() => selectedAddress);
    if (address !== null && !list.some((station) => station.address === address)) {
      // A pending station operation (for example a channel write) still owns
      // the busy flags; keep the selection and editor alive so the result
      // stays visible. The merge re-adds the station when the write settles.
      if (!store.stationBusy(address)) {
        selectedAddress = null;
        channelEditorOpen = false;
        store.clearChannelEditorFeedback();
      }
    }
    const renaming = untrack(() => store.editingAddress);
    if (renaming !== null && !list.some((station) => station.address === renaming)) {
      store.cancelRename();
    }
  });

  // The two drawers are mutually exclusive: opening station details must
  // replace the settings drawer so the overlays never stack on each other.
  $effect(() => {
    if (selectedAddress !== null) settingsOpen = false;
  });

  onMount(() => {
    store.mount();
  });

  onDestroy(() => {
    store.dispose();
    clearToasts();
  });

  function openDetails(address: string) {
    selectedAddress = address;
  }

  function closeDetails() {
    selectedAddress = null;
  }

  function openSettings() {
    selectedAddress = null;
    settingsOpen = true;
  }

  function closeSettings() {
    settingsOpen = false;
  }

  function handleLanguageChanged() {
    clearToasts();
    store.onLocaleChanged();
  }

  function openChannelEditor() {
    store.clearChannelEditorFeedback();
    channelEditorOpen = true;
  }

  function closeChannelEditor() {
    if (selectedStation && store.stationBusy(selectedStation.address)) return;
    channelEditorOpen = false;
  }

  function cancelBulkPower() {
    bulkConfirmTarget = null;
  }

  async function confirmBulkPower() {
    const state = bulkConfirmTarget;
    bulkConfirmTarget = null;
    if (state) await store.runBulkPower(state);
  }

  function handleGlobalKeydown(event: KeyboardEvent) {
    if (event.key !== 'Escape') return;
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
    if (channelEditorOpen) {
      closeChannelEditor();
    } else if (bulkConfirmTarget) {
      cancelBulkPower();
    } else if (settingsOpen) {
      closeSettings();
    } else if (selectedAddress) {
      selectedAddress = null;
    }
  }
</script>

<svelte:window onkeydown={handleGlobalKeydown} />

<div class="app-container" inert={selectedStation !== null || settingsOpen || bulkConfirmTarget !== null}>
  <AppHeader
    scanning={store.scanRunning}
    isBulkLoading={store.isBulkLoading}
    cancellingBulk={store.cancellingBulk}
    scanLocked={store.scanLocked}
    bulkLocked={store.bulkLocked}
    bulkTarget={store.bulkTarget}
    canOn={store.actionableOn.length > 0}
    canStandby={store.actionableStandby.length > 0}
    canSleep={store.actionableSleep.length > 0}
    allOn={store.allOn}
    allStandby={store.allStandby}
    allSleep={store.allSleep}
    onCount={store.fleetOn}
    standbyCount={store.fleetStandby}
    sleepCount={store.fleetSleep}
    unverifiedCount={store.fleetUnverified}
    knownCount={store.stations.length}
    untrustedCount={store.untrustedCount}
    onScan={() => void store.startScan()}
    onStop={() => void store.stopScan()}
    stopping={store.stoppingScan}
    onBulkPower={(state) => store.requestBulkPower(state)}
    onCancelBulk={() => void store.cancelBulkPower()}
    onOpenSettings={openSettings}
  />

  <main>
    <FleetView
      stations={store.sortedStations}
      channelOf={(station) => store.displayChannel(station)}
      selectedAddress={selectedAddress}
      conflictDetails={store.conflictDetails}
      scanError={store.scanError}
      isLoading={store.isLoading}
      externalScanning={store.externalScanning}
      scanElapsed={store.scanElapsed}
      editingAddress={store.editingAddress}
      feedbackByAddress={store.powerFeedbackMap}
      pendingTargetByAddress={store.powerTargetByAddress}
      gattBusyAddresses={store.gattOperations}
      configBusyAddresses={store.configOperations}
      gattLockedByAddress={store.gattLockedByAddress}
      stationLocked={store.stationLocked}
      onSelect={openDetails}
      onPower={(station, state) => void store.setPower(station, state)}
      onOpenDetails={(station) => openDetails(station.address)}
      onStartRename={(station) => store.startRename(station)}
      onSaveRename={(station, name) => void store.saveRename(station, name)}
      onCancelRename={() => store.cancelRename()}
    />
  </main>

  <StatusFooter
    statusMessage={store.statusMessage}
    apiRunning={store.apiRunning}
    apiError={store.apiError}
    apiAddress={store.apiAddress}
    configWarnings={store.configWarnings}
    configWritable={store.configWritable}
  />
</div>

{#if selectedStation}
  <div
    class="scrim"
    role="presentation"
    transition:fade={dur({ duration: 200 })}
    onclick={closeDetails}
  ></div>
  <DetailsDrawer
    station={selectedStation}
    busy={store.busyByAddress.get(selectedStation.address) ?? false}
    locked={store.gattLockedByAddress.get(selectedStation.address) ?? false}
    inactive={channelEditorOpen}
    onClose={closeDetails}
    onRefresh={(station) => void store.refreshCapabilities(station)}
    onIdentify={(station) => void store.identify(station)}
    onOpenChannelEditor={openChannelEditor}
  />
{/if}

{#if settingsOpen}
  <div
    class="scrim"
    role="presentation"
    transition:fade={dur({ duration: 200 })}
    onclick={closeSettings}
  ></div>
  <SettingsPanel
    inactive={channelEditorOpen || Boolean(bulkConfirmTarget)}
    onClose={closeSettings}
    onLanguageChanged={handleLanguageChanged}
    onStatusPollIntervalChanged={(intervalSeconds) => {
      store.setStatusPollIntervalSeconds(intervalSeconds);
      void store.refreshStationProjection();
    }}
    onStatusPollingEnabledChanged={(enabled) => store.setStatusPollingEnabled(enabled)}
    onStationProjectionChanged={() => void store.refreshStationProjection()}
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
      occupiedChannels={occupiedChannels}
      hasUnknownVisibleChannel={hasUnknownVisibleChannel}
      error={store.channelError}
      warning={store.channelWarning}
      busy={store.gattOperations.has(selectedStation.address) || store.configOperations.has(selectedStation.address)}
      saving={store.channelSavingAddress === selectedStation.address}
      locked={store.gattLockedByAddress.get(selectedStation.address) ?? false}
      onClose={closeChannelEditor}
      onSave={(channel, allowUnknownConflictRisk) => void store.saveChannel(selectedStation, channel, allowUnknownConflictRisk)}
      onIdentify={(station) => void store.identify(station)}
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
      visibleCount={store.visibleCount}
      invisibleCount={store.invisibleCount}
      uncertainCount={store.uncertainCount}
      actionableCount={bulkConfirmTarget === 'on' ? store.actionableOn.length : bulkConfirmTarget === 'standby' ? store.actionableStandby.length : store.actionableSleep.length}
      busy={store.isBulkLoading || store.bulkLocked || store.scanningActive}
      onCancel={cancelBulkPower}
      onConfirm={confirmBulkPower}
    />
  </div>
{/if}

<Toast />

<style>
  .app-container { display: flex; flex-direction: column; height: 100vh; }
  main { flex: 1; padding: var(--spacing-md) var(--spacing-md) var(--spacing-lg); overflow: auto; }
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
</style>
