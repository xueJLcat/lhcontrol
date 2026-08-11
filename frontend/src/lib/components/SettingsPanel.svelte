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
  import { languagePreference, setLanguagePreference, t, type LanguagePreference } from '../i18n.svelte';
  import SettingsDrawer from './SettingsDrawer.svelte';
  import { AsyncSetting } from './settings/async-setting.svelte';

  let {
    inactive = false,
    onClose,
    onLanguageChanged = () => {},
    onStatusPollIntervalChanged = () => {},
    onStatusPollingEnabledChanged = () => {},
    onStationProjectionChanged = () => {}
  }: {
    inactive?: boolean;
    onClose: () => void;
    onLanguageChanged?: () => void;
    onStatusPollIntervalChanged?: (intervalSeconds: number) => void;
    onStatusPollingEnabledChanged?: (enabled: boolean) => void;
    onStationProjectionChanged?: () => void;
  } = $props();

  let adapters = $state<bluetoothModels.AdapterInfo[]>([]);
  let loading = $state(false);
  let loadError = $state<string | null>(null);
  let languageBusy = $state(false);

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

  const autoSleepSetting = new AsyncSetting({
    getter: GetAutoSleepSettings,
    setter: SetAutoSleepSettings,
    map: (settings) => autosleepModels.Settings.createFrom(settings),
    loadMessage: 'Auto-sleep settings could not be loaded',
    saveMessage: 'Auto-sleep settings could not be saved'
  });
  const scanOnStartupSetting = new AsyncSetting({
    getter: GetScanOnStartup, setter: SetScanOnStartup,
    loadMessage: 'Startup scan setting could not be loaded', saveMessage: 'Startup scan setting could not be saved'
  });
  const scanDurationSetting = new AsyncSetting({
    getter: GetScanDurationSeconds, setter: SetScanDurationSeconds,
    loadMessage: 'Scan duration could not be loaded', saveMessage: 'Scan duration could not be saved'
  });
  const statusPollingEnabledSetting = new AsyncSetting({
    getter: GetStatusPollingEnabled, setter: SetStatusPollingEnabled,
    loadMessage: 'Automatic station refresh setting could not be loaded',
    saveMessage: 'Automatic station refresh setting could not be saved',
    afterSave: (enabled) => onStatusPollingEnabledChanged(enabled)
  });
  const bulkPowerTimeoutSetting = new AsyncSetting({
    getter: GetBulkPowerTimeoutSeconds, setter: SetBulkPowerTimeoutSeconds,
    loadMessage: 'Bulk power timeout could not be loaded', saveMessage: 'Bulk power timeout could not be saved'
  });
  const statusPollIntervalSetting = new AsyncSetting({
    getter: GetStatusPollIntervalSeconds, setter: SetStatusPollIntervalSeconds,
    loadMessage: 'Status polling interval could not be loaded', saveMessage: 'Status polling interval could not be saved',
    afterSave: (intervalSeconds) => onStatusPollIntervalChanged(intervalSeconds)
  });
  const stationOperationTimeoutSetting = new AsyncSetting({
    getter: GetStationOperationTimeoutSeconds, setter: SetStationOperationTimeoutSeconds,
    loadMessage: 'Station operation timeout could not be loaded', saveMessage: 'Station operation timeout could not be saved'
  });
  const powerConfirmAttemptsOnSetting = new AsyncSetting({
    getter: GetPowerConfirmAttemptsOn, setter: SetPowerConfirmAttemptsOn,
    loadMessage: powerTimingLoad, saveMessage: powerTimingSave
  });
  const powerConfirmAttemptsOffSetting = new AsyncSetting({
    getter: GetPowerConfirmAttemptsOff, setter: SetPowerConfirmAttemptsOff,
    loadMessage: powerTimingLoad, saveMessage: powerTimingSave
  });
  const powerConfirmPollIntervalSetting = new AsyncSetting({
    getter: GetPowerConfirmPollIntervalMs, setter: SetPowerConfirmPollIntervalMs,
    loadMessage: powerTimingLoad, saveMessage: powerTimingSave
  });
  const bootFallbackSetting = new AsyncSetting({
    getter: GetBootFallbackSeconds, setter: SetBootFallbackSeconds,
    loadMessage: 'Boot fallback window could not be loaded', saveMessage: 'Boot fallback window could not be saved'
  });
  const sleepFinalWriteTimeoutSetting = new AsyncSetting({
    getter: GetSleepFinalWriteTimeoutSeconds, setter: SetSleepFinalWriteTimeoutSeconds,
    loadMessage: powerTimingLoad, saveMessage: powerTimingSave
  });
  const sleepPrepareGapSetting = new AsyncSetting({
    getter: GetSleepPrepareGapMs, setter: SetSleepPrepareGapMs,
    loadMessage: powerTimingLoad, saveMessage: powerTimingSave
  });
  const discoveryAttemptsSetting = new AsyncSetting({
    getter: GetDiscoveryAttempts, setter: SetDiscoveryAttempts,
    loadMessage: connectionTimingLoad, saveMessage: connectionTimingSave
  });
  const discoveryRetryDelaySetting = new AsyncSetting({
    getter: GetDiscoveryRetryDelayMs, setter: SetDiscoveryRetryDelayMs,
    loadMessage: connectionTimingLoad, saveMessage: connectionTimingSave
  });
  const apiListenAddressSetting = new AsyncSetting({
    getter: GetAPIListenAddress, setter: SetAPIListenAddress,
    loadMessage: 'HTTP API settings could not be loaded', saveMessage: 'HTTP API settings could not be saved'
  });
  const powerWriteAttempts = new AsyncSetting({ getter: GetPowerWriteAttempts, setter: SetPowerWriteAttempts, loadMessage: powerTimingLoad, saveMessage: powerTimingSave });
  const operationRetryDelay = new AsyncSetting({ getter: GetOperationRetryDelayMs, setter: SetOperationRetryDelayMs, loadMessage: powerTimingLoad, saveMessage: powerTimingSave });
  const identifyAttempts = new AsyncSetting({ getter: GetIdentifyAttempts, setter: SetIdentifyAttempts, loadMessage: connectionTimingLoad, saveMessage: connectionTimingSave });
  const confirmReconnectThreshold = new AsyncSetting({ getter: GetConfirmReconnectThreshold, setter: SetConfirmReconnectThreshold, loadMessage: connectionTimingLoad, saveMessage: connectionTimingSave });
  const confirmReconnectDelay = new AsyncSetting({ getter: GetConfirmReconnectDelayMs, setter: SetConfirmReconnectDelayMs, loadMessage: connectionTimingLoad, saveMessage: connectionTimingSave });
  const channelConfirmAttempts = new AsyncSetting({ getter: GetChannelConfirmAttempts, setter: SetChannelConfirmAttempts, loadMessage: channelPresenceLoad, saveMessage: channelPresenceSave });
  const channelConfirmInterval = new AsyncSetting({ getter: GetChannelConfirmIntervalMs, setter: SetChannelConfirmIntervalMs, loadMessage: channelPresenceLoad, saveMessage: channelPresenceSave });
  const presenceMissThreshold = new AsyncSetting({
    getter: GetPresenceMissThreshold, setter: SetPresenceMissThreshold,
    loadMessage: channelPresenceLoad, saveMessage: channelPresenceSave,
    afterSave: () => onStationProjectionChanged()
  });
  const initialReadTimeout = new AsyncSetting({ getter: GetInitialReadTimeoutSeconds, setter: SetInitialReadTimeoutSeconds, loadMessage: readBudgetLoad, saveMessage: readBudgetSave });
  const scanReadPhaseTimeout = new AsyncSetting({ getter: GetScanReadPhaseTimeoutSeconds, setter: SetScanReadPhaseTimeoutSeconds, loadMessage: readBudgetLoad, saveMessage: readBudgetSave });
  const statusReadTimeout = new AsyncSetting({ getter: GetStatusReadTimeoutSeconds, setter: SetStatusReadTimeoutSeconds, loadMessage: readBudgetLoad, saveMessage: readBudgetSave });
  const statusRefreshTimeout = new AsyncSetting({ getter: GetStatusRefreshTimeoutSeconds, setter: SetStatusRefreshTimeoutSeconds, loadMessage: readBudgetLoad, saveMessage: readBudgetSave });
  const channelScanFreshness = new AsyncSetting({
    getter: GetChannelScanFreshnessSeconds, setter: SetChannelScanFreshnessSeconds,
    loadMessage: readBudgetLoad, saveMessage: readBudgetSave,
    afterSave: () => onStationProjectionChanged()
  });
  const recoveryRetryBase = new AsyncSetting({ getter: GetRecoveryRetryBaseSeconds, setter: SetRecoveryRetryBaseSeconds, loadMessage: recoveryLoad, saveMessage: recoverySave });
  const recoveryRetryMax = new AsyncSetting({ getter: GetRecoveryRetryMaxSeconds, setter: SetRecoveryRetryMaxSeconds, loadMessage: recoveryLoad, saveMessage: recoverySave });
  const absentStationRetryLimit = new AsyncSetting({ getter: GetAbsentStationRetryLimit, setter: SetAbsentStationRetryLimit, loadMessage: recoveryLoad, saveMessage: recoverySave });
  const bluetoothInitRetry = new AsyncSetting({ getter: GetBluetoothInitRetrySeconds, setter: SetBluetoothInitRetrySeconds, loadMessage: recoveryLoad, saveMessage: recoverySave });

  const settingsToLoad: Array<{ load: () => Promise<void> }> = [
    autoSleepSetting, scanOnStartupSetting, scanDurationSetting, statusPollingEnabledSetting,
    bulkPowerTimeoutSetting, statusPollIntervalSetting, stationOperationTimeoutSetting,
    powerConfirmAttemptsOnSetting, powerConfirmAttemptsOffSetting, powerConfirmPollIntervalSetting,
    bootFallbackSetting, sleepFinalWriteTimeoutSetting, sleepPrepareGapSetting,
    discoveryAttemptsSetting, discoveryRetryDelaySetting, apiListenAddressSetting,
    powerWriteAttempts, operationRetryDelay, identifyAttempts, confirmReconnectThreshold,
    confirmReconnectDelay, channelConfirmAttempts, channelConfirmInterval, presenceMissThreshold,
    initialReadTimeout, scanReadPhaseTimeout, statusReadTimeout, statusRefreshTimeout,
    channelScanFreshness, recoveryRetryBase, recoveryRetryMax, absentStationRetryLimit, bluetoothInitRetry
  ];

  onMount(() => {
    void loadAdapterSettings();
    for (const setting of settingsToLoad) void setting.load();
  });

  async function loadAdapterSettings() {
    if (loading) return;
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


  const drawerModel = {
    preferences: {
      get autoSleep() { return autoSleepSetting.value; },
      get autoSleepError() { return autoSleepSetting.error; },
      get autoSleepBusy() { return autoSleepSetting.busy; },
      get scanOnStartup() { return scanOnStartupSetting.value; },
      get scanOnStartupError() { return scanOnStartupSetting.error; },
      get scanOnStartupBusy() { return scanOnStartupSetting.busy; },
      get scanDurationSeconds() { return scanDurationSetting.value; },
      get scanDurationError() { return scanDurationSetting.error; },
      get scanDurationBusy() { return scanDurationSetting.busy; },
      get statusPollingEnabled() { return statusPollingEnabledSetting.value; },
      get statusPollingEnabledError() { return statusPollingEnabledSetting.error; },
      get statusPollingEnabledBusy() { return statusPollingEnabledSetting.busy; },
      get statusPollIntervalSeconds() { return statusPollIntervalSetting.value; },
      get statusPollIntervalError() { return statusPollIntervalSetting.error; },
      get statusPollIntervalBusy() { return statusPollIntervalSetting.busy; },
      get languagePreference() { return languagePreference(); },
      get languageBusy() { return languageBusy; },
      onAutoSleepChange: autoSleepSetting.change,
      onAutoSleepRetry: autoSleepSetting.load,
      onScanOnStartupChange: scanOnStartupSetting.change,
      onScanOnStartupRetry: scanOnStartupSetting.load,
      onScanDurationChange: scanDurationSetting.change,
      onScanDurationRetry: scanDurationSetting.load,
      onStatusPollingEnabledChange: statusPollingEnabledSetting.change,
      onStatusPollingEnabledRetry: statusPollingEnabledSetting.load,
      onStatusPollIntervalChange: statusPollIntervalSetting.change,
      onStatusPollIntervalRetry: statusPollIntervalSetting.load,
      onLanguageChange: changeLanguage
    },
    operations: {
      get bulkPowerTimeoutSeconds() { return bulkPowerTimeoutSetting.value; },
      get bulkPowerTimeoutError() { return bulkPowerTimeoutSetting.error; },
      get bulkPowerTimeoutBusy() { return bulkPowerTimeoutSetting.busy; },
      get stationOperationTimeoutSeconds() { return stationOperationTimeoutSetting.value; },
      get stationOperationTimeoutError() { return stationOperationTimeoutSetting.error; },
      get stationOperationTimeoutBusy() { return stationOperationTimeoutSetting.busy; },
      get powerConfirmAttemptsOn() { return powerConfirmAttemptsOnSetting.value; },
      get powerConfirmAttemptsOnError() { return powerConfirmAttemptsOnSetting.error; },
      get powerConfirmAttemptsOnBusy() { return powerConfirmAttemptsOnSetting.busy; },
      get powerConfirmAttemptsOff() { return powerConfirmAttemptsOffSetting.value; },
      get powerConfirmAttemptsOffError() { return powerConfirmAttemptsOffSetting.error; },
      get powerConfirmAttemptsOffBusy() { return powerConfirmAttemptsOffSetting.busy; },
      get powerConfirmPollIntervalMs() { return powerConfirmPollIntervalSetting.value; },
      get powerConfirmPollIntervalError() { return powerConfirmPollIntervalSetting.error; },
      get powerConfirmPollIntervalBusy() { return powerConfirmPollIntervalSetting.busy; },
      get bootFallbackSeconds() { return bootFallbackSetting.value; },
      get bootFallbackError() { return bootFallbackSetting.error; },
      get bootFallbackBusy() { return bootFallbackSetting.busy; },
      get sleepFinalWriteTimeoutSeconds() { return sleepFinalWriteTimeoutSetting.value; },
      get sleepFinalWriteTimeoutError() { return sleepFinalWriteTimeoutSetting.error; },
      get sleepFinalWriteTimeoutBusy() { return sleepFinalWriteTimeoutSetting.busy; },
      get sleepPrepareGapMs() { return sleepPrepareGapSetting.value; },
      get sleepPrepareGapError() { return sleepPrepareGapSetting.error; },
      get sleepPrepareGapBusy() { return sleepPrepareGapSetting.busy; },
      get discoveryAttempts() { return discoveryAttemptsSetting.value; },
      get discoveryAttemptsError() { return discoveryAttemptsSetting.error; },
      get discoveryAttemptsBusy() { return discoveryAttemptsSetting.busy; },
      get discoveryRetryDelayMs() { return discoveryRetryDelaySetting.value; },
      get discoveryRetryDelayError() { return discoveryRetryDelaySetting.error; },
      get discoveryRetryDelayBusy() { return discoveryRetryDelaySetting.busy; },
      get powerWriteAttempts() { return powerWriteAttempts.value; },
      get powerWriteAttemptsError() { return powerWriteAttempts.error; },
      get powerWriteAttemptsBusy() { return powerWriteAttempts.busy; },
      get operationRetryDelayMs() { return operationRetryDelay.value; },
      get operationRetryDelayError() { return operationRetryDelay.error; },
      get operationRetryDelayBusy() { return operationRetryDelay.busy; },
      get identifyAttempts() { return identifyAttempts.value; },
      get identifyAttemptsError() { return identifyAttempts.error; },
      get identifyAttemptsBusy() { return identifyAttempts.busy; },
      get confirmReconnectThreshold() { return confirmReconnectThreshold.value; },
      get confirmReconnectThresholdError() { return confirmReconnectThreshold.error; },
      get confirmReconnectThresholdBusy() { return confirmReconnectThreshold.busy; },
      get confirmReconnectDelayMs() { return confirmReconnectDelay.value; },
      get confirmReconnectDelayError() { return confirmReconnectDelay.error; },
      get confirmReconnectDelayBusy() { return confirmReconnectDelay.busy; },
      onBulkPowerTimeoutChange: bulkPowerTimeoutSetting.change,
      onBulkPowerTimeoutRetry: bulkPowerTimeoutSetting.load,
      onStationOperationTimeoutChange: stationOperationTimeoutSetting.change,
      onStationOperationTimeoutRetry: stationOperationTimeoutSetting.load,
      onPowerConfirmAttemptsOnChange: powerConfirmAttemptsOnSetting.change,
      onPowerConfirmAttemptsOnRetry: powerConfirmAttemptsOnSetting.load,
      onPowerConfirmAttemptsOffChange: powerConfirmAttemptsOffSetting.change,
      onPowerConfirmAttemptsOffRetry: powerConfirmAttemptsOffSetting.load,
      onPowerConfirmPollIntervalChange: powerConfirmPollIntervalSetting.change,
      onPowerConfirmPollIntervalRetry: powerConfirmPollIntervalSetting.load,
      onBootFallbackChange: bootFallbackSetting.change,
      onBootFallbackRetry: bootFallbackSetting.load,
      onSleepFinalWriteTimeoutChange: sleepFinalWriteTimeoutSetting.change,
      onSleepFinalWriteTimeoutRetry: sleepFinalWriteTimeoutSetting.load,
      onSleepPrepareGapChange: sleepPrepareGapSetting.change,
      onSleepPrepareGapRetry: sleepPrepareGapSetting.load,
      onDiscoveryAttemptsChange: discoveryAttemptsSetting.change,
      onDiscoveryAttemptsRetry: discoveryAttemptsSetting.load,
      onDiscoveryRetryDelayChange: discoveryRetryDelaySetting.change,
      onDiscoveryRetryDelayRetry: discoveryRetryDelaySetting.load,
      onPowerWriteAttemptsChange: powerWriteAttempts.change,
      onPowerWriteAttemptsRetry: powerWriteAttempts.load,
      onOperationRetryDelayChange: operationRetryDelay.change,
      onOperationRetryDelayRetry: operationRetryDelay.load,
      onIdentifyAttemptsChange: identifyAttempts.change,
      onIdentifyAttemptsRetry: identifyAttempts.load,
      onConfirmReconnectThresholdChange: confirmReconnectThreshold.change,
      onConfirmReconnectThresholdRetry: confirmReconnectThreshold.load,
      onConfirmReconnectDelayChange: confirmReconnectDelay.change,
      onConfirmReconnectDelayRetry: confirmReconnectDelay.load
    },
    advanced: {
      get apiListenAddress() { return apiListenAddressSetting.value; },
      get apiListenAddressError() { return apiListenAddressSetting.error; },
      get apiListenAddressBusy() { return apiListenAddressSetting.busy; },
      get channelConfirmAttempts() { return channelConfirmAttempts.value; },
      get channelConfirmAttemptsError() { return channelConfirmAttempts.error; },
      get channelConfirmAttemptsBusy() { return channelConfirmAttempts.busy; },
      get channelConfirmIntervalMs() { return channelConfirmInterval.value; },
      get channelConfirmIntervalError() { return channelConfirmInterval.error; },
      get channelConfirmIntervalBusy() { return channelConfirmInterval.busy; },
      get presenceMissThreshold() { return presenceMissThreshold.value; },
      get presenceMissThresholdError() { return presenceMissThreshold.error; },
      get presenceMissThresholdBusy() { return presenceMissThreshold.busy; },
      get initialReadTimeoutSeconds() { return initialReadTimeout.value; },
      get initialReadTimeoutError() { return initialReadTimeout.error; },
      get initialReadTimeoutBusy() { return initialReadTimeout.busy; },
      get scanReadPhaseTimeoutSeconds() { return scanReadPhaseTimeout.value; },
      get scanReadPhaseTimeoutError() { return scanReadPhaseTimeout.error; },
      get scanReadPhaseTimeoutBusy() { return scanReadPhaseTimeout.busy; },
      get statusReadTimeoutSeconds() { return statusReadTimeout.value; },
      get statusReadTimeoutError() { return statusReadTimeout.error; },
      get statusReadTimeoutBusy() { return statusReadTimeout.busy; },
      get statusRefreshTimeoutSeconds() { return statusRefreshTimeout.value; },
      get statusRefreshTimeoutError() { return statusRefreshTimeout.error; },
      get statusRefreshTimeoutBusy() { return statusRefreshTimeout.busy; },
      get channelScanFreshnessSeconds() { return channelScanFreshness.value; },
      get channelScanFreshnessError() { return channelScanFreshness.error; },
      get channelScanFreshnessBusy() { return channelScanFreshness.busy; },
      get recoveryRetryBaseSeconds() { return recoveryRetryBase.value; },
      get recoveryRetryBaseError() { return recoveryRetryBase.error; },
      get recoveryRetryBaseBusy() { return recoveryRetryBase.busy; },
      get recoveryRetryMaxSeconds() { return recoveryRetryMax.value; },
      get recoveryRetryMaxError() { return recoveryRetryMax.error; },
      get recoveryRetryMaxBusy() { return recoveryRetryMax.busy; },
      get absentStationRetryLimit() { return absentStationRetryLimit.value; },
      get absentStationRetryLimitError() { return absentStationRetryLimit.error; },
      get absentStationRetryLimitBusy() { return absentStationRetryLimit.busy; },
      get bluetoothInitRetrySeconds() { return bluetoothInitRetry.value; },
      get bluetoothInitRetryError() { return bluetoothInitRetry.error; },
      get bluetoothInitRetryBusy() { return bluetoothInitRetry.busy; },
      onAPIListenAddressChange: apiListenAddressSetting.change,
      onAPIListenAddressRetry: apiListenAddressSetting.load,
      onChannelConfirmAttemptsChange: channelConfirmAttempts.change,
      onChannelConfirmAttemptsRetry: channelConfirmAttempts.load,
      onChannelConfirmIntervalChange: channelConfirmInterval.change,
      onChannelConfirmIntervalRetry: channelConfirmInterval.load,
      onPresenceMissThresholdChange: presenceMissThreshold.change,
      onPresenceMissThresholdRetry: presenceMissThreshold.load,
      onInitialReadTimeoutChange: initialReadTimeout.change,
      onInitialReadTimeoutRetry: initialReadTimeout.load,
      onScanReadPhaseTimeoutChange: scanReadPhaseTimeout.change,
      onScanReadPhaseTimeoutRetry: scanReadPhaseTimeout.load,
      onStatusReadTimeoutChange: statusReadTimeout.change,
      onStatusReadTimeoutRetry: statusReadTimeout.load,
      onStatusRefreshTimeoutChange: statusRefreshTimeout.change,
      onStatusRefreshTimeoutRetry: statusRefreshTimeout.load,
      onChannelScanFreshnessChange: channelScanFreshness.change,
      onChannelScanFreshnessRetry: channelScanFreshness.load,
      onRecoveryRetryBaseChange: recoveryRetryBase.change,
      onRecoveryRetryBaseRetry: recoveryRetryBase.load,
      onRecoveryRetryMaxChange: recoveryRetryMax.change,
      onRecoveryRetryMaxRetry: recoveryRetryMax.load,
      onAbsentStationRetryLimitChange: absentStationRetryLimit.change,
      onAbsentStationRetryLimitRetry: absentStationRetryLimit.load,
      onBluetoothInitRetryChange: bluetoothInitRetry.change,
      onBluetoothInitRetryRetry: bluetoothInitRetry.load
    },
    diagnostics: {
      get adapters() { return adapters; },
      get loading() { return loading; },
      get loadError() { return loadError; },
      onRefresh: loadAdapterSettings
    }
  };
</script>

<SettingsDrawer model={drawerModel} {inactive} {onClose} />
