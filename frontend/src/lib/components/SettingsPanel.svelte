<script lang="ts">
  import { onMount } from 'svelte';
  import {
    GetAbsentStationRetryLimit,
    GetAPIListenAddress,
    GetAutoSleepSettings,
    GetBluetoothInitRetrySeconds,
    GetBootFallbackSeconds,
    GetBulkPowerTimeoutSeconds,
    GetChannelConfirmAttempts,
    GetChannelConfirmIntervalMs,
    GetChannelScanFreshnessSeconds,
    GetConfirmReconnectDelayMs,
    GetConfirmReconnectThreshold,
    GetDiscoveryAttempts,
    GetDiscoveryRetryDelayMs,
    GetIdentifyAttempts,
    GetInitialReadTimeoutSeconds,
    GetOperationRetryDelayMs,
    GetPowerConfirmAttemptsOff,
    GetPowerConfirmAttemptsOn,
    GetPowerConfirmPollIntervalMs,
    GetPowerWriteAttempts,
    GetPresenceMissThreshold,
    GetRecoveryRetryBaseSeconds,
    GetRecoveryRetryMaxSeconds,
    GetScanDurationSeconds,
    GetScanOnStartup,
    GetScanReadPhaseTimeoutSeconds,
    GetSleepFinalWriteTimeoutSeconds,
    GetSleepPrepareGapMs,
    GetStationOperationTimeoutSeconds,
    GetStatusPollIntervalSeconds,
    GetStatusPollingEnabled,
    GetStatusReadTimeoutSeconds,
    GetStatusRefreshTimeoutSeconds,
    ListBluetoothAdapters,
    SetAbsentStationRetryLimit,
    SetAPIListenAddress,
    SetAutoSleepSettings,
    SetBluetoothInitRetrySeconds,
    SetBootFallbackSeconds,
    SetBulkPowerTimeoutSeconds,
    SetChannelConfirmAttempts,
    SetChannelConfirmIntervalMs,
    SetChannelScanFreshnessSeconds,
    SetConfirmReconnectDelayMs,
    SetConfirmReconnectThreshold,
    SetDiscoveryAttempts,
    SetDiscoveryRetryDelayMs,
    SetIdentifyAttempts,
    SetInitialReadTimeoutSeconds,
    SetLanguage,
    SetOperationRetryDelayMs,
    SetPowerConfirmAttemptsOff,
    SetPowerConfirmAttemptsOn,
    SetPowerConfirmPollIntervalMs,
    SetPowerWriteAttempts,
    SetPresenceMissThreshold,
    SetRecoveryRetryBaseSeconds,
    SetRecoveryRetryMaxSeconds,
    SetScanDurationSeconds,
    SetScanOnStartup,
    SetScanReadPhaseTimeoutSeconds,
    SetSleepFinalWriteTimeoutSeconds,
    SetSleepPrepareGapMs,
    SetStationOperationTimeoutSeconds,
    SetStatusPollIntervalSeconds,
    SetStatusPollingEnabled,
    SetStatusReadTimeoutSeconds,
    SetStatusRefreshTimeoutSeconds
  } from '../backend';
  import { autosleep as autosleepModels, bluetooth as bluetoothModels } from '../../../wailsjs/go/models';
  import { pushToast } from '../toast';
  import {
    languagePreference, setLanguagePreference, t, withDetail, type LanguagePreference, type TranslationKey
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
  let stationOperationTimeoutSeconds = $state<number | null>(null);
  let stationOperationTimeoutError = $state<string | null>(null);
  let stationOperationTimeoutBusy = $state(false);
  let powerConfirmAttemptsOn = $state<number | null>(null);
  let powerConfirmAttemptsOnError = $state<string | null>(null);
  let powerConfirmAttemptsOnBusy = $state(false);
  let powerConfirmAttemptsOff = $state<number | null>(null);
  let powerConfirmAttemptsOffError = $state<string | null>(null);
  let powerConfirmAttemptsOffBusy = $state(false);
  let powerConfirmPollIntervalMs = $state<number | null>(null);
  let powerConfirmPollIntervalError = $state<string | null>(null);
  let powerConfirmPollIntervalBusy = $state(false);
  let bootFallbackSeconds = $state<number | null>(null);
  let bootFallbackError = $state<string | null>(null);
  let bootFallbackBusy = $state(false);
  let sleepFinalWriteTimeoutSeconds = $state<number | null>(null);
  let sleepFinalWriteTimeoutError = $state<string | null>(null);
  let sleepFinalWriteTimeoutBusy = $state(false);
  let sleepPrepareGapMs = $state<number | null>(null);
  let sleepPrepareGapError = $state<string | null>(null);
  let sleepPrepareGapBusy = $state(false);
  let discoveryAttempts = $state<number | null>(null);
  let discoveryAttemptsError = $state<string | null>(null);
  let discoveryAttemptsBusy = $state(false);
  let discoveryRetryDelayMs = $state<number | null>(null);
  let discoveryRetryDelayError = $state<string | null>(null);
  let discoveryRetryDelayBusy = $state(false);
  let apiListenAddress = $state<string | null>(null);
  let apiListenAddressError = $state<string | null>(null);
  let apiListenAddressBusy = $state(false);
  let languageBusy = $state(false);

  // Shared lifecycle for the advanced numeric settings: optimistic update,
  // rollback on failure, and toast feedback, without repeating the pattern
  // seventeen times.
  class NumberSetting {
    value = $state<number | null>(null);
    error = $state<string | null>(null);
    busy = $state(false);
    loadMessage: TranslationKey;
    saveMessage: TranslationKey;

    constructor(loadMessage: TranslationKey, saveMessage: TranslationKey) {
      this.loadMessage = loadMessage;
      this.saveMessage = saveMessage;
    }

    async load(getter: () => Promise<number>) {
      this.error = null;
      try {
        this.value = await getter();
      } catch (error) {
        this.value = null;
        this.error = String(error);
        pushToast(withDetail(this.loadMessage, error));
      }
    }

    async change(next: number, setter: (value: number) => Promise<void>) {
      if (this.busy || this.value === null) return;
      const previous = this.value;
      this.value = next;
      this.busy = true;
      try {
        await setter(next);
      } catch (error) {
        this.value = previous;
        pushToast(withDetail(this.saveMessage, error));
      } finally {
        this.busy = false;
      }
    }
  }

  const powerTimingLoad = 'Power confirmation settings could not be loaded';
  const powerTimingSave = 'Power confirmation settings could not be saved';
  const connectionTimingLoad = 'Connection timing settings could not be loaded';
  const connectionTimingSave = 'Connection timing settings could not be saved';
  const channelPresenceLoad = 'Channel and presence settings could not be loaded';
  const channelPresenceSave = 'Channel and presence settings could not be saved';
  const readBudgetLoad = 'Read budget settings could not be loaded';
  const readBudgetSave = 'Read budget settings could not be saved';
  const recoveryLoad = 'Recovery settings could not be loaded';
  const recoverySave = 'Recovery settings could not be saved';

  const powerWriteAttempts = new NumberSetting(powerTimingLoad, powerTimingSave);
  const operationRetryDelay = new NumberSetting(powerTimingLoad, powerTimingSave);
  const identifyAttempts = new NumberSetting(connectionTimingLoad, connectionTimingSave);
  const confirmReconnectThreshold = new NumberSetting(connectionTimingLoad, connectionTimingSave);
  const confirmReconnectDelay = new NumberSetting(connectionTimingLoad, connectionTimingSave);
  const channelConfirmAttempts = new NumberSetting(channelPresenceLoad, channelPresenceSave);
  const channelConfirmInterval = new NumberSetting(channelPresenceLoad, channelPresenceSave);
  const presenceMissThreshold = new NumberSetting(channelPresenceLoad, channelPresenceSave);
  const initialReadTimeout = new NumberSetting(readBudgetLoad, readBudgetSave);
  const scanReadPhaseTimeout = new NumberSetting(readBudgetLoad, readBudgetSave);
  const statusReadTimeout = new NumberSetting(readBudgetLoad, readBudgetSave);
  const statusRefreshTimeout = new NumberSetting(readBudgetLoad, readBudgetSave);
  const channelScanFreshness = new NumberSetting(readBudgetLoad, readBudgetSave);
  const recoveryRetryBase = new NumberSetting(recoveryLoad, recoverySave);
  const recoveryRetryMax = new NumberSetting(recoveryLoad, recoverySave);
  const absentStationRetryLimit = new NumberSetting(recoveryLoad, recoverySave);
  const bluetoothInitRetry = new NumberSetting(recoveryLoad, recoverySave);

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
    void loadStationOperationTimeout();
    void loadPowerConfirmAttemptsOn();
    void loadPowerConfirmAttemptsOff();
    void loadPowerConfirmPollInterval();
    void loadBootFallback();
    void loadSleepFinalWriteTimeout();
    void loadSleepPrepareGap();
    void loadDiscoveryAttempts();
    void loadDiscoveryRetryDelay();
    void loadAPIListenAddress();
    void powerWriteAttempts.load(GetPowerWriteAttempts);
    void operationRetryDelay.load(GetOperationRetryDelayMs);
    void identifyAttempts.load(GetIdentifyAttempts);
    void confirmReconnectThreshold.load(GetConfirmReconnectThreshold);
    void confirmReconnectDelay.load(GetConfirmReconnectDelayMs);
    void channelConfirmAttempts.load(GetChannelConfirmAttempts);
    void channelConfirmInterval.load(GetChannelConfirmIntervalMs);
    void presenceMissThreshold.load(GetPresenceMissThreshold);
    void initialReadTimeout.load(GetInitialReadTimeoutSeconds);
    void scanReadPhaseTimeout.load(GetScanReadPhaseTimeoutSeconds);
    void statusReadTimeout.load(GetStatusReadTimeoutSeconds);
    void statusRefreshTimeout.load(GetStatusRefreshTimeoutSeconds);
    void channelScanFreshness.load(GetChannelScanFreshnessSeconds);
    void recoveryRetryBase.load(GetRecoveryRetryBaseSeconds);
    void recoveryRetryMax.load(GetRecoveryRetryMaxSeconds);
    void absentStationRetryLimit.load(GetAbsentStationRetryLimit);
    void bluetoothInitRetry.load(GetBluetoothInitRetrySeconds);
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

  async function loadStationOperationTimeout() {
    stationOperationTimeoutError = null;
    try {
      stationOperationTimeoutSeconds = await GetStationOperationTimeoutSeconds();
    } catch (error) {
      stationOperationTimeoutSeconds = null;
      stationOperationTimeoutError = String(error);
      pushToast(withDetail('Station operation timeout could not be loaded', error));
    }
  }

  async function changeStationOperationTimeout(next: number) {
    if (stationOperationTimeoutBusy || stationOperationTimeoutSeconds === null) return;
    const previous = stationOperationTimeoutSeconds;
    stationOperationTimeoutSeconds = next;
    stationOperationTimeoutBusy = true;
    try {
      await SetStationOperationTimeoutSeconds(next);
    } catch (error) {
      stationOperationTimeoutSeconds = previous;
      pushToast(withDetail('Station operation timeout could not be saved', error));
    } finally {
      stationOperationTimeoutBusy = false;
    }
  }

  async function loadPowerConfirmAttemptsOn() {
    powerConfirmAttemptsOnError = null;
    try {
      powerConfirmAttemptsOn = await GetPowerConfirmAttemptsOn();
    } catch (error) {
      powerConfirmAttemptsOn = null;
      powerConfirmAttemptsOnError = String(error);
      pushToast(withDetail('Power confirmation settings could not be loaded', error));
    }
  }

  async function changePowerConfirmAttemptsOn(next: number) {
    if (powerConfirmAttemptsOnBusy || powerConfirmAttemptsOn === null) return;
    const previous = powerConfirmAttemptsOn;
    powerConfirmAttemptsOn = next;
    powerConfirmAttemptsOnBusy = true;
    try {
      await SetPowerConfirmAttemptsOn(next);
    } catch (error) {
      powerConfirmAttemptsOn = previous;
      pushToast(withDetail('Power confirmation settings could not be saved', error));
    } finally {
      powerConfirmAttemptsOnBusy = false;
    }
  }

  async function loadPowerConfirmAttemptsOff() {
    powerConfirmAttemptsOffError = null;
    try {
      powerConfirmAttemptsOff = await GetPowerConfirmAttemptsOff();
    } catch (error) {
      powerConfirmAttemptsOff = null;
      powerConfirmAttemptsOffError = String(error);
      pushToast(withDetail('Power confirmation settings could not be loaded', error));
    }
  }

  async function changePowerConfirmAttemptsOff(next: number) {
    if (powerConfirmAttemptsOffBusy || powerConfirmAttemptsOff === null) return;
    const previous = powerConfirmAttemptsOff;
    powerConfirmAttemptsOff = next;
    powerConfirmAttemptsOffBusy = true;
    try {
      await SetPowerConfirmAttemptsOff(next);
    } catch (error) {
      powerConfirmAttemptsOff = previous;
      pushToast(withDetail('Power confirmation settings could not be saved', error));
    } finally {
      powerConfirmAttemptsOffBusy = false;
    }
  }

  async function loadPowerConfirmPollInterval() {
    powerConfirmPollIntervalError = null;
    try {
      powerConfirmPollIntervalMs = await GetPowerConfirmPollIntervalMs();
    } catch (error) {
      powerConfirmPollIntervalMs = null;
      powerConfirmPollIntervalError = String(error);
      pushToast(withDetail('Power confirmation settings could not be loaded', error));
    }
  }

  async function changePowerConfirmPollInterval(next: number) {
    if (powerConfirmPollIntervalBusy || powerConfirmPollIntervalMs === null) return;
    const previous = powerConfirmPollIntervalMs;
    powerConfirmPollIntervalMs = next;
    powerConfirmPollIntervalBusy = true;
    try {
      await SetPowerConfirmPollIntervalMs(next);
    } catch (error) {
      powerConfirmPollIntervalMs = previous;
      pushToast(withDetail('Power confirmation settings could not be saved', error));
    } finally {
      powerConfirmPollIntervalBusy = false;
    }
  }

  async function loadBootFallback() {
    bootFallbackError = null;
    try {
      bootFallbackSeconds = await GetBootFallbackSeconds();
    } catch (error) {
      bootFallbackSeconds = null;
      bootFallbackError = String(error);
      pushToast(withDetail('Boot fallback window could not be loaded', error));
    }
  }

  async function changeBootFallback(next: number) {
    if (bootFallbackBusy || bootFallbackSeconds === null) return;
    const previous = bootFallbackSeconds;
    bootFallbackSeconds = next;
    bootFallbackBusy = true;
    try {
      await SetBootFallbackSeconds(next);
    } catch (error) {
      bootFallbackSeconds = previous;
      pushToast(withDetail('Boot fallback window could not be saved', error));
    } finally {
      bootFallbackBusy = false;
    }
  }

  async function loadSleepFinalWriteTimeout() {
    sleepFinalWriteTimeoutError = null;
    try {
      sleepFinalWriteTimeoutSeconds = await GetSleepFinalWriteTimeoutSeconds();
    } catch (error) {
      sleepFinalWriteTimeoutSeconds = null;
      sleepFinalWriteTimeoutError = String(error);
      pushToast(withDetail(powerTimingLoad, error));
    }
  }

  async function changeSleepFinalWriteTimeout(next: number) {
    if (sleepFinalWriteTimeoutBusy || sleepFinalWriteTimeoutSeconds === null) return;
    const previous = sleepFinalWriteTimeoutSeconds;
    sleepFinalWriteTimeoutSeconds = next;
    sleepFinalWriteTimeoutBusy = true;
    try {
      await SetSleepFinalWriteTimeoutSeconds(next);
    } catch (error) {
      sleepFinalWriteTimeoutSeconds = previous;
      pushToast(withDetail(powerTimingSave, error));
    } finally {
      sleepFinalWriteTimeoutBusy = false;
    }
  }

  async function loadSleepPrepareGap() {
    sleepPrepareGapError = null;
    try {
      sleepPrepareGapMs = await GetSleepPrepareGapMs();
    } catch (error) {
      sleepPrepareGapMs = null;
      sleepPrepareGapError = String(error);
      pushToast(withDetail(powerTimingLoad, error));
    }
  }

  async function changeSleepPrepareGap(next: number) {
    if (sleepPrepareGapBusy || sleepPrepareGapMs === null) return;
    const previous = sleepPrepareGapMs;
    sleepPrepareGapMs = next;
    sleepPrepareGapBusy = true;
    try {
      await SetSleepPrepareGapMs(next);
    } catch (error) {
      sleepPrepareGapMs = previous;
      pushToast(withDetail(powerTimingSave, error));
    } finally {
      sleepPrepareGapBusy = false;
    }
  }

  async function loadDiscoveryAttempts() {
    discoveryAttemptsError = null;
    try {
      discoveryAttempts = await GetDiscoveryAttempts();
    } catch (error) {
      discoveryAttempts = null;
      discoveryAttemptsError = String(error);
      pushToast(withDetail('Connection timing settings could not be loaded', error));
    }
  }

  async function changeDiscoveryAttempts(next: number) {
    if (discoveryAttemptsBusy || discoveryAttempts === null) return;
    const previous = discoveryAttempts;
    discoveryAttempts = next;
    discoveryAttemptsBusy = true;
    try {
      await SetDiscoveryAttempts(next);
    } catch (error) {
      discoveryAttempts = previous;
      pushToast(withDetail('Connection timing settings could not be saved', error));
    } finally {
      discoveryAttemptsBusy = false;
    }
  }

  async function loadDiscoveryRetryDelay() {
    discoveryRetryDelayError = null;
    try {
      discoveryRetryDelayMs = await GetDiscoveryRetryDelayMs();
    } catch (error) {
      discoveryRetryDelayMs = null;
      discoveryRetryDelayError = String(error);
      pushToast(withDetail('Connection timing settings could not be loaded', error));
    }
  }

  async function changeDiscoveryRetryDelay(next: number) {
    if (discoveryRetryDelayBusy || discoveryRetryDelayMs === null) return;
    const previous = discoveryRetryDelayMs;
    discoveryRetryDelayMs = next;
    discoveryRetryDelayBusy = true;
    try {
      await SetDiscoveryRetryDelayMs(next);
    } catch (error) {
      discoveryRetryDelayMs = previous;
      pushToast(withDetail('Connection timing settings could not be saved', error));
    } finally {
      discoveryRetryDelayBusy = false;
    }
  }

  async function loadAPIListenAddress() {
    apiListenAddressError = null;
    try {
      apiListenAddress = await GetAPIListenAddress();
    } catch (error) {
      apiListenAddress = null;
      apiListenAddressError = String(error);
      pushToast(withDetail('HTTP API settings could not be loaded', error));
    }
  }

  async function changeAPIListenAddress(next: string) {
    if (apiListenAddressBusy || apiListenAddress === null || next === apiListenAddress) return;
    const previous = apiListenAddress;
    apiListenAddress = next;
    apiListenAddressBusy = true;
    try {
      await SetAPIListenAddress(next);
    } catch (error) {
      apiListenAddress = previous;
      pushToast(withDetail('HTTP API settings could not be saved', error));
    } finally {
      apiListenAddressBusy = false;
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
  {stationOperationTimeoutSeconds}
  {stationOperationTimeoutError}
  {stationOperationTimeoutBusy}
  {powerConfirmAttemptsOn}
  {powerConfirmAttemptsOnError}
  {powerConfirmAttemptsOnBusy}
  {powerConfirmAttemptsOff}
  {powerConfirmAttemptsOffError}
  {powerConfirmAttemptsOffBusy}
  {powerConfirmPollIntervalMs}
  {powerConfirmPollIntervalError}
  {powerConfirmPollIntervalBusy}
  {bootFallbackSeconds}
  {bootFallbackError}
  {bootFallbackBusy}
  {sleepFinalWriteTimeoutSeconds}
  {sleepFinalWriteTimeoutError}
  {sleepFinalWriteTimeoutBusy}
  {sleepPrepareGapMs}
  {sleepPrepareGapError}
  {sleepPrepareGapBusy}
  {discoveryAttempts}
  {discoveryAttemptsError}
  {discoveryAttemptsBusy}
  {discoveryRetryDelayMs}
  {discoveryRetryDelayError}
  {discoveryRetryDelayBusy}
  {apiListenAddress}
  {apiListenAddressError}
  {apiListenAddressBusy}
  powerWriteAttempts={powerWriteAttempts.value}
  powerWriteAttemptsError={powerWriteAttempts.error}
  powerWriteAttemptsBusy={powerWriteAttempts.busy}
  operationRetryDelayMs={operationRetryDelay.value}
  operationRetryDelayError={operationRetryDelay.error}
  operationRetryDelayBusy={operationRetryDelay.busy}
  identifyAttempts={identifyAttempts.value}
  identifyAttemptsError={identifyAttempts.error}
  identifyAttemptsBusy={identifyAttempts.busy}
  confirmReconnectThreshold={confirmReconnectThreshold.value}
  confirmReconnectThresholdError={confirmReconnectThreshold.error}
  confirmReconnectThresholdBusy={confirmReconnectThreshold.busy}
  confirmReconnectDelayMs={confirmReconnectDelay.value}
  confirmReconnectDelayError={confirmReconnectDelay.error}
  confirmReconnectDelayBusy={confirmReconnectDelay.busy}
  channelConfirmAttempts={channelConfirmAttempts.value}
  channelConfirmAttemptsError={channelConfirmAttempts.error}
  channelConfirmAttemptsBusy={channelConfirmAttempts.busy}
  channelConfirmIntervalMs={channelConfirmInterval.value}
  channelConfirmIntervalError={channelConfirmInterval.error}
  channelConfirmIntervalBusy={channelConfirmInterval.busy}
  presenceMissThreshold={presenceMissThreshold.value}
  presenceMissThresholdError={presenceMissThreshold.error}
  presenceMissThresholdBusy={presenceMissThreshold.busy}
  initialReadTimeoutSeconds={initialReadTimeout.value}
  initialReadTimeoutError={initialReadTimeout.error}
  initialReadTimeoutBusy={initialReadTimeout.busy}
  scanReadPhaseTimeoutSeconds={scanReadPhaseTimeout.value}
  scanReadPhaseTimeoutError={scanReadPhaseTimeout.error}
  scanReadPhaseTimeoutBusy={scanReadPhaseTimeout.busy}
  statusReadTimeoutSeconds={statusReadTimeout.value}
  statusReadTimeoutError={statusReadTimeout.error}
  statusReadTimeoutBusy={statusReadTimeout.busy}
  statusRefreshTimeoutSeconds={statusRefreshTimeout.value}
  statusRefreshTimeoutError={statusRefreshTimeout.error}
  statusRefreshTimeoutBusy={statusRefreshTimeout.busy}
  channelScanFreshnessSeconds={channelScanFreshness.value}
  channelScanFreshnessError={channelScanFreshness.error}
  channelScanFreshnessBusy={channelScanFreshness.busy}
  recoveryRetryBaseSeconds={recoveryRetryBase.value}
  recoveryRetryBaseError={recoveryRetryBase.error}
  recoveryRetryBaseBusy={recoveryRetryBase.busy}
  recoveryRetryMaxSeconds={recoveryRetryMax.value}
  recoveryRetryMaxError={recoveryRetryMax.error}
  recoveryRetryMaxBusy={recoveryRetryMax.busy}
  absentStationRetryLimit={absentStationRetryLimit.value}
  absentStationRetryLimitError={absentStationRetryLimit.error}
  absentStationRetryLimitBusy={absentStationRetryLimit.busy}
  bluetoothInitRetrySeconds={bluetoothInitRetry.value}
  bluetoothInitRetryError={bluetoothInitRetry.error}
  bluetoothInitRetryBusy={bluetoothInitRetry.busy}
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
  onStationOperationTimeoutChange={changeStationOperationTimeout}
  onStationOperationTimeoutRetry={loadStationOperationTimeout}
  onPowerConfirmAttemptsOnChange={changePowerConfirmAttemptsOn}
  onPowerConfirmAttemptsOnRetry={loadPowerConfirmAttemptsOn}
  onPowerConfirmAttemptsOffChange={changePowerConfirmAttemptsOff}
  onPowerConfirmAttemptsOffRetry={loadPowerConfirmAttemptsOff}
  onPowerConfirmPollIntervalChange={changePowerConfirmPollInterval}
  onPowerConfirmPollIntervalRetry={loadPowerConfirmPollInterval}
  onBootFallbackChange={changeBootFallback}
  onBootFallbackRetry={loadBootFallback}
  onSleepFinalWriteTimeoutChange={changeSleepFinalWriteTimeout}
  onSleepFinalWriteTimeoutRetry={loadSleepFinalWriteTimeout}
  onSleepPrepareGapChange={changeSleepPrepareGap}
  onSleepPrepareGapRetry={loadSleepPrepareGap}
  onDiscoveryAttemptsChange={changeDiscoveryAttempts}
  onDiscoveryAttemptsRetry={loadDiscoveryAttempts}
  onDiscoveryRetryDelayChange={changeDiscoveryRetryDelay}
  onDiscoveryRetryDelayRetry={loadDiscoveryRetryDelay}
  onAPIListenAddressChange={changeAPIListenAddress}
  onAPIListenAddressRetry={loadAPIListenAddress}
  onPowerWriteAttemptsChange={(next) => powerWriteAttempts.change(next, SetPowerWriteAttempts)}
  onPowerWriteAttemptsRetry={() => powerWriteAttempts.load(GetPowerWriteAttempts)}
  onOperationRetryDelayChange={(next) => operationRetryDelay.change(next, SetOperationRetryDelayMs)}
  onOperationRetryDelayRetry={() => operationRetryDelay.load(GetOperationRetryDelayMs)}
  onIdentifyAttemptsChange={(next) => identifyAttempts.change(next, SetIdentifyAttempts)}
  onIdentifyAttemptsRetry={() => identifyAttempts.load(GetIdentifyAttempts)}
  onConfirmReconnectThresholdChange={(next) => confirmReconnectThreshold.change(next, SetConfirmReconnectThreshold)}
  onConfirmReconnectThresholdRetry={() => confirmReconnectThreshold.load(GetConfirmReconnectThreshold)}
  onConfirmReconnectDelayChange={(next) => confirmReconnectDelay.change(next, SetConfirmReconnectDelayMs)}
  onConfirmReconnectDelayRetry={() => confirmReconnectDelay.load(GetConfirmReconnectDelayMs)}
  onChannelConfirmAttemptsChange={(next) => channelConfirmAttempts.change(next, SetChannelConfirmAttempts)}
  onChannelConfirmAttemptsRetry={() => channelConfirmAttempts.load(GetChannelConfirmAttempts)}
  onChannelConfirmIntervalChange={(next) => channelConfirmInterval.change(next, SetChannelConfirmIntervalMs)}
  onChannelConfirmIntervalRetry={() => channelConfirmInterval.load(GetChannelConfirmIntervalMs)}
  onPresenceMissThresholdChange={(next) => presenceMissThreshold.change(next, SetPresenceMissThreshold)}
  onPresenceMissThresholdRetry={() => presenceMissThreshold.load(GetPresenceMissThreshold)}
  onInitialReadTimeoutChange={(next) => initialReadTimeout.change(next, SetInitialReadTimeoutSeconds)}
  onInitialReadTimeoutRetry={() => initialReadTimeout.load(GetInitialReadTimeoutSeconds)}
  onScanReadPhaseTimeoutChange={(next) => scanReadPhaseTimeout.change(next, SetScanReadPhaseTimeoutSeconds)}
  onScanReadPhaseTimeoutRetry={() => scanReadPhaseTimeout.load(GetScanReadPhaseTimeoutSeconds)}
  onStatusReadTimeoutChange={(next) => statusReadTimeout.change(next, SetStatusReadTimeoutSeconds)}
  onStatusReadTimeoutRetry={() => statusReadTimeout.load(GetStatusReadTimeoutSeconds)}
  onStatusRefreshTimeoutChange={(next) => statusRefreshTimeout.change(next, SetStatusRefreshTimeoutSeconds)}
  onStatusRefreshTimeoutRetry={() => statusRefreshTimeout.load(GetStatusRefreshTimeoutSeconds)}
  onChannelScanFreshnessChange={(next) => channelScanFreshness.change(next, SetChannelScanFreshnessSeconds)}
  onChannelScanFreshnessRetry={() => channelScanFreshness.load(GetChannelScanFreshnessSeconds)}
  onRecoveryRetryBaseChange={(next) => recoveryRetryBase.change(next, SetRecoveryRetryBaseSeconds)}
  onRecoveryRetryBaseRetry={() => recoveryRetryBase.load(GetRecoveryRetryBaseSeconds)}
  onRecoveryRetryMaxChange={(next) => recoveryRetryMax.change(next, SetRecoveryRetryMaxSeconds)}
  onRecoveryRetryMaxRetry={() => recoveryRetryMax.load(GetRecoveryRetryMaxSeconds)}
  onAbsentStationRetryLimitChange={(next) => absentStationRetryLimit.change(next, SetAbsentStationRetryLimit)}
  onAbsentStationRetryLimitRetry={() => absentStationRetryLimit.load(GetAbsentStationRetryLimit)}
  onBluetoothInitRetryChange={(next) => bluetoothInitRetry.change(next, SetBluetoothInitRetrySeconds)}
  onBluetoothInitRetryRetry={() => bluetoothInitRetry.load(GetBluetoothInitRetrySeconds)}
  onLanguageChange={changeLanguage}
/>
