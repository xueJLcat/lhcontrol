<script lang="ts">
  import { onMount } from 'svelte';
  import {
    GetAutoSleepSettings,
    ListBluetoothAdapters,
    SetAutoSleepSettings,
    SetLanguage
  } from '../backend';
  import { autosleep as autosleepModels, bluetooth as bluetoothModels } from '../../../wailsjs/go/models';
  import { pushToast } from '../toast';
  import {
    languagePreference, setLanguagePreference, t, withDetail, type LanguagePreference
  } from '../i18n.svelte';
  import SettingsDrawer from './SettingsDrawer.svelte';

  let { inactive = false, onClose, onLanguageChanged = () => {} }: { inactive?: boolean; onClose: () => void; onLanguageChanged?: () => void } = $props();

  let adapters = $state<bluetoothModels.AdapterInfo[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let autoSleepSettings = $state<autosleepModels.Settings | null>(null);
  let autoSleepError = $state<string | null>(null);
  let autoSleepBusy = $state(false);
  let languageBusy = $state(false);

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
      pushToast(withDetail('Auto-sleep settings could not be loaded', error));
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
      pushToast(withDetail('Auto-sleep settings could not be saved', error));
    } finally {
      autoSleepBusy = false;
    }
  }

  async function changeLanguage(next: LanguagePreference) {
    if (languageBusy || next === languagePreference()) return;
    const previous = languagePreference();
    setLanguagePreference(next);
    onLanguageChanged();
    languageBusy = true;
    try {
      await SetLanguage(next === 'system' ? '' : next);
    } catch (error) {
      setLanguagePreference(previous);
      onLanguageChanged();
      pushToast(`${t('Language setting could not be saved')}: ${String(error)}`);
    } finally {
      languageBusy = false;
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
  languagePreference={languagePreference()}
  {languageBusy}
  {inactive}
  {onClose}
  onRefresh={loadAdapterSettings}
  onAutoSleepChange={changeAutoSleep}
  onAutoSleepRetry={loadAutoSleepSettings}
  onLanguageChange={changeLanguage}
/>
