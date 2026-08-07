<script lang="ts">
  import { onMount } from 'svelte';
  import {
    GetAutoSleepSettings,
    GetBulkPowerTimeoutSeconds,
    GetScanDurationSeconds,
    GetScanOnStartup,
    GetStatusPollIntervalSeconds,
    GetStatusPollingEnabled,
    ListBluetoothAdapters,
    SetAutoSleepSettings,
    SetBulkPowerTimeoutSeconds,
    SetLanguage,
    SetScanDurationSeconds,
    SetScanOnStartup,
    SetStatusPollIntervalSeconds,
    SetStatusPollingEnabled
  } from '../backend';
  import { autosleep as autosleepModels, bluetooth as bluetoothModels } from '../../../wailsjs/go/models';
  import { pushToast } from '../toast';
  import {
    languagePreference, setLanguagePreference, t, withDetail, type LanguagePreference
  } from '../i18n.svelte';
  import SettingsDrawer from './SettingsDrawer.svelte';

  let {
    inactive = false,
    onClose,
    onLanguageChanged = () => {},
    onStatusPollIntervalChanged = () => {},
    onStatusPollingEnabledChanged = () => {}
  }: {
    inactive?: boolean;
    onClose: () => void;
    onLanguageChanged?: () => void;
    onStatusPollIntervalChanged?: (intervalSeconds: number) => void;
    onStatusPollingEnabledChanged?: (enabled: boolean) => void;
  } = $props();

  let adapters = $state<bluetoothModels.AdapterInfo[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let autoSleepSettings = $state<autosleepModels.Settings | null>(null);
  let autoSleepError = $state<string | null>(null);
  let autoSleepBusy = $state(false);
  let scanOnStartup = $state<boolean | null>(null);
  let scanOnStartupError = $state<string | null>(null);
  let scanOnStartupBusy = $state(false);
  let scanDurationSeconds = $state<number | null>(null);
  let scanDurationError = $state<string | null>(null);
  let scanDurationBusy = $state(false);
  let statusPollingEnabled = $state<boolean | null>(null);
  let statusPollingEnabledError = $state<string | null>(null);
  let statusPollingEnabledBusy = $state(false);
  let bulkPowerTimeoutSeconds = $state<number | null>(null);
  let bulkPowerTimeoutError = $state<string | null>(null);
  let bulkPowerTimeoutBusy = $state(false);
  let statusPollIntervalSeconds = $state<number | null>(null);
  let statusPollIntervalError = $state<string | null>(null);
  let statusPollIntervalBusy = $state(false);
  let languageBusy = $state(false);

  // The panel only mounts while the settings drawer is open, so loading here
  // matches the previous open-time loading in App.
  onMount(() => {
    void loadAdapterSettings();
    void loadAutoSleepSettings();
    void loadScanOnStartup();
    void loadScanDuration();
    void loadStatusPollingEnabled();
    void loadBulkPowerTimeout();
    void loadStatusPollInterval();
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

  async function loadBulkPowerTimeout() {
    bulkPowerTimeoutError = null;
    try {
      bulkPowerTimeoutSeconds = await GetBulkPowerTimeoutSeconds();
    } catch (error) {
      bulkPowerTimeoutSeconds = null;
      bulkPowerTimeoutError = String(error);
      pushToast(withDetail('Bulk power timeout could not be loaded', error));
    }
  }

  async function loadScanOnStartup() {
    scanOnStartupError = null;
    try {
      scanOnStartup = await GetScanOnStartup();
    } catch (error) {
      scanOnStartup = null;
      scanOnStartupError = String(error);
      pushToast(withDetail('Startup scan setting could not be loaded', error));
    }
  }

  async function changeScanOnStartup(next: boolean) {
    if (scanOnStartupBusy || scanOnStartup === null) return;
    const previous = scanOnStartup;
    scanOnStartup = next;
    scanOnStartupBusy = true;
    try {
      await SetScanOnStartup(next);
    } catch (error) {
      scanOnStartup = previous;
      pushToast(withDetail('Startup scan setting could not be saved', error));
    } finally {
      scanOnStartupBusy = false;
    }
  }

  async function loadScanDuration() {
    scanDurationError = null;
    try {
      scanDurationSeconds = await GetScanDurationSeconds();
    } catch (error) {
      scanDurationSeconds = null;
      scanDurationError = String(error);
      pushToast(withDetail('Scan duration could not be loaded', error));
    }
  }

  async function changeScanDuration(next: number) {
    if (scanDurationBusy || scanDurationSeconds === null) return;
    const previous = scanDurationSeconds;
    scanDurationSeconds = next;
    scanDurationBusy = true;
    try {
      await SetScanDurationSeconds(next);
    } catch (error) {
      scanDurationSeconds = previous;
      pushToast(withDetail('Scan duration could not be saved', error));
    } finally {
      scanDurationBusy = false;
    }
  }

  async function loadStatusPollingEnabled() {
    statusPollingEnabledError = null;
    try {
      statusPollingEnabled = await GetStatusPollingEnabled();
    } catch (error) {
      statusPollingEnabled = null;
      statusPollingEnabledError = String(error);
      pushToast(withDetail('Automatic station refresh setting could not be loaded', error));
    }
  }

  async function changeStatusPollingEnabled(next: boolean) {
    if (statusPollingEnabledBusy || statusPollingEnabled === null) return;
    const previous = statusPollingEnabled;
    statusPollingEnabled = next;
    statusPollingEnabledBusy = true;
    try {
      await SetStatusPollingEnabled(next);
      onStatusPollingEnabledChanged(next);
    } catch (error) {
      statusPollingEnabled = previous;
      pushToast(withDetail('Automatic station refresh setting could not be saved', error));
    } finally {
      statusPollingEnabledBusy = false;
    }
  }

  async function changeBulkPowerTimeout(next: number) {
    if (bulkPowerTimeoutBusy || bulkPowerTimeoutSeconds === null) return;
    const previous = bulkPowerTimeoutSeconds;
    bulkPowerTimeoutSeconds = next;
    bulkPowerTimeoutBusy = true;
    try {
      await SetBulkPowerTimeoutSeconds(next);
    } catch (error) {
      bulkPowerTimeoutSeconds = previous;
      pushToast(withDetail('Bulk power timeout could not be saved', error));
    } finally {
      bulkPowerTimeoutBusy = false;
    }
  }

  async function loadStatusPollInterval() {
    statusPollIntervalError = null;
    try {
      statusPollIntervalSeconds = await GetStatusPollIntervalSeconds();
    } catch (error) {
      statusPollIntervalSeconds = null;
      statusPollIntervalError = String(error);
      pushToast(withDetail('Status polling interval could not be loaded', error));
    }
  }

  async function changeStatusPollInterval(next: number) {
    if (statusPollIntervalBusy || statusPollIntervalSeconds === null) return;
    const previous = statusPollIntervalSeconds;
    statusPollIntervalSeconds = next;
    statusPollIntervalBusy = true;
    try {
      await SetStatusPollIntervalSeconds(next);
      onStatusPollIntervalChanged(next);
    } catch (error) {
      statusPollIntervalSeconds = previous;
      pushToast(withDetail('Status polling interval could not be saved', error));
    } finally {
      statusPollIntervalBusy = false;
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
  {scanOnStartup}
  {scanOnStartupError}
  {scanOnStartupBusy}
  {scanDurationSeconds}
  {scanDurationError}
  {scanDurationBusy}
  {statusPollingEnabled}
  {statusPollingEnabledError}
  {statusPollingEnabledBusy}
  {bulkPowerTimeoutSeconds}
  {bulkPowerTimeoutError}
  {bulkPowerTimeoutBusy}
  {statusPollIntervalSeconds}
  {statusPollIntervalError}
  {statusPollIntervalBusy}
  languagePreference={languagePreference()}
  {languageBusy}
  {inactive}
  {onClose}
  onRefresh={loadAdapterSettings}
  onAutoSleepChange={changeAutoSleep}
  onAutoSleepRetry={loadAutoSleepSettings}
  onScanOnStartupChange={changeScanOnStartup}
  onScanOnStartupRetry={loadScanOnStartup}
  onScanDurationChange={changeScanDuration}
  onScanDurationRetry={loadScanDuration}
  onStatusPollingEnabledChange={changeStatusPollingEnabled}
  onStatusPollingEnabledRetry={loadStatusPollingEnabled}
  onBulkPowerTimeoutChange={changeBulkPowerTimeout}
  onBulkPowerTimeoutRetry={loadBulkPowerTimeout}
  onStatusPollIntervalChange={changeStatusPollInterval}
  onStatusPollIntervalRetry={loadStatusPollInterval}
  onLanguageChange={changeLanguage}
/>
