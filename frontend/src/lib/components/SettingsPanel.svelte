<script lang="ts">
  import { onMount } from 'svelte';
  import {
    GetAutoSleepSettings,
    ListBluetoothAdapters,
    SetAutoSleepSettings
  } from '../backend';
  import { autosleep as autosleepModels, bluetooth as bluetoothModels } from '../../../wailsjs/go/models';
  import { pushToast } from '../toast';
  import SettingsDrawer from './SettingsDrawer.svelte';

  let { inactive = false, onClose }: { inactive?: boolean; onClose: () => void } = $props();

  let adapters = $state<bluetoothModels.AdapterInfo[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let autoSleepSettings = $state<autosleepModels.Settings | null>(null);
  let autoSleepError = $state<string | null>(null);
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
      adapters = await ListBluetoothAdapters() ?? [];
    } catch (error) {
      loadError = String(error);
    } finally {
      loading = false;
    }
  }

  async function loadAutoSleepSettings() {
    autoSleepError = null;
    try {
      autoSleepSettings = autosleepModels.Settings.createFrom(await GetAutoSleepSettings());
    } catch (error) {
      autoSleepSettings = null;
      autoSleepError = String(error);
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
  autoSleep={autoSleepSettings}
  autoSleepError={autoSleepError}
  {autoSleepBusy}
  {inactive}
  {onClose}
  onRefresh={loadAdapterSettings}
  onAutoSleepChange={changeAutoSleep}
  onAutoSleepRetry={loadAutoSleepSettings}
/>
