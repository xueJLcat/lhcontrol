<script lang="ts">
  import { onMount } from 'svelte';
  import {
    GetAutoSleepSettings,
    GetBluetoothAdapter,
    ListBluetoothAdapters,
    SetAutoSleepSettings,
    SetBluetoothAdapter
  } from '../backend';
  import { autosleep as autosleepModels, bluetooth as bluetoothModels } from '../../../wailsjs/go/models';
  import { pushToast } from '../toast';
  import SettingsDrawer from './SettingsDrawer.svelte';

  let { inactive = false, onClose }: { inactive?: boolean; onClose: () => void } = $props();

  let adapters = $state<bluetoothModels.AdapterInfo[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let preferredAdapterId = $state('');
  let adapterSaving = $state(false);
  let autoSleepSettings = $state<autosleepModels.Settings | null>(null);
  let autoSleepBusy = $state(false);

  // The panel only mounts while the settings drawer is open, so loading here
  // matches the previous open-time loading in App.
  onMount(() => {
    void loadAdapterSettings();
    void loadAutoSleepSettings();
  });

  async function loadAdapterSettings() {
    loading = true;
    loadError = null;
    try {
      const [list, current] = await Promise.all([ListBluetoothAdapters(), GetBluetoothAdapter()]);
      adapters = list ?? [];
      preferredAdapterId = current ?? '';
    } catch (error) {
      loadError = String(error);
    } finally {
      loading = false;
    }
  }

  async function selectAdapter(deviceId: string) {
    if (adapterSaving || deviceId === preferredAdapterId) return;
    const previous = preferredAdapterId;
    // Optimistic update keeps the radio responsive; failures roll back.
    preferredAdapterId = deviceId;
    adapterSaving = true;
    try {
      await SetBluetoothAdapter(deviceId);
      pushToast(deviceId === '' ? 'Bluetooth adapter preference cleared.' : 'Bluetooth adapter preference saved.', 'success');
    } catch (error) {
      preferredAdapterId = previous;
      pushToast(`Bluetooth adapter preference could not be saved: ${String(error)}`);
    } finally {
      adapterSaving = false;
    }
  }

  async function loadAutoSleepSettings() {
    try {
      autoSleepSettings = autosleepModels.Settings.createFrom(await GetAutoSleepSettings());
    } catch (error) {
      autoSleepSettings = null;
      pushToast(`Auto-sleep settings could not be loaded: ${String(error)}`);
    }
  }

  async function changeAutoSleep(next: autosleepModels.Settings) {
    if (autoSleepBusy || !autoSleepSettings) return;
    const previous = autoSleepSettings;
    // Optimistic update keeps the controls responsive; failures roll back.
    autoSleepSettings = next;
    autoSleepBusy = true;
    try {
      await SetAutoSleepSettings(next);
    } catch (error) {
      autoSleepSettings = previous;
      pushToast(`Auto-sleep settings could not be saved: ${String(error)}`);
    } finally {
      autoSleepBusy = false;
    }
  }
</script>

<SettingsDrawer
  adapters={adapters}
  {loading}
  loadError={loadError}
  selectedDeviceId={preferredAdapterId}
  busy={adapterSaving}
  autoSleep={autoSleepSettings}
  {autoSleepBusy}
  {inactive}
  {onClose}
  onRefresh={loadAdapterSettings}
  onSelect={selectAdapter}
  onAutoSleepChange={changeAutoSleep}
/>
