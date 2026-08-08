<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { fly } from 'svelte/transition';
  import { Bluetooth, Cable, Gauge, History, Hourglass, Languages, LoaderCircle, MoonStar, Network, Radar, RefreshCw, Shuffle, Timer, X } from 'lucide-svelte';
  import { autosleep } from '../../../wailsjs/go/models';
  import type { bluetooth } from '../../../wailsjs/go/models';
  import { focusTrap } from '../actions';
  import { dur } from '../motion';
  import { t, type LanguagePreference } from '../i18n.svelte';

  const MIN_DELAY_MINUTES = 1;
  const MAX_DELAY_MINUTES = 120;
  const MIN_SCAN_DURATION_SECONDS = 2;
  const MAX_SCAN_DURATION_SECONDS = 30;
  const MIN_BULK_TIMEOUT_SECONDS = 30;
  const MAX_BULK_TIMEOUT_SECONDS = 600;
  const MIN_STATUS_POLL_INTERVAL_SECONDS = 5;
  const MAX_STATUS_POLL_INTERVAL_SECONDS = 300;
  // Mirrors internal/config bounds; keep them in sync when ranges change.
  const MIN_STATION_TIMEOUT_SECONDS = 30;
  const MAX_STATION_TIMEOUT_SECONDS = 120;
  const MIN_POWER_CONFIRM_ATTEMPTS = 5;
  const MAX_POWER_CONFIRM_ATTEMPTS = 200;
  const MIN_POWER_CONFIRM_POLL_MS = 50;
  const MAX_POWER_CONFIRM_POLL_MS = 2000;
  const MIN_BOOT_FALLBACK_SECONDS = 2;
  const MAX_BOOT_FALLBACK_SECONDS = 60;
  const MIN_SLEEP_FINAL_WRITE_SECONDS = 5;
  const MAX_SLEEP_FINAL_WRITE_SECONDS = 120;
  const MIN_SLEEP_PREPARE_GAP_MS = 0;
  const MAX_SLEEP_PREPARE_GAP_MS = 500;
  const MIN_DISCOVERY_ATTEMPTS = 1;
  const MAX_DISCOVERY_ATTEMPTS = 10;
  const MIN_DISCOVERY_RETRY_DELAY_MS = 100;
  const MAX_DISCOVERY_RETRY_DELAY_MS = 5000;
  const MIN_POWER_WRITE_ATTEMPTS = 1;
  const MAX_POWER_WRITE_ATTEMPTS = 5;
  const MIN_OPERATION_RETRY_DELAY_MS = 100;
  const MAX_OPERATION_RETRY_DELAY_MS = 5000;
  const MIN_IDENTIFY_ATTEMPTS = 1;
  const MAX_IDENTIFY_ATTEMPTS = 5;
  const MIN_CONFIRM_RECONNECT_THRESHOLD = 1;
  const MAX_CONFIRM_RECONNECT_THRESHOLD = 5;
  const MIN_CONFIRM_RECONNECT_DELAY_MS = 50;
  const MAX_CONFIRM_RECONNECT_DELAY_MS = 2000;
  const MIN_CHANNEL_CONFIRM_ATTEMPTS = 1;
  const MAX_CHANNEL_CONFIRM_ATTEMPTS = 20;
  const MIN_CHANNEL_CONFIRM_INTERVAL_MS = 50;
  const MAX_CHANNEL_CONFIRM_INTERVAL_MS = 2000;
  const MIN_PRESENCE_MISS_THRESHOLD = 1;
  const MAX_PRESENCE_MISS_THRESHOLD = 10;
  const MIN_INITIAL_READ_TIMEOUT_SECONDS = 10;
  const MAX_INITIAL_READ_TIMEOUT_SECONDS = 60;
  const MIN_SCAN_READ_PHASE_TIMEOUT_SECONDS = 15;
  const MAX_SCAN_READ_PHASE_TIMEOUT_SECONDS = 120;
  const MIN_STATUS_READ_TIMEOUT_SECONDS = 5;
  const MAX_STATUS_READ_TIMEOUT_SECONDS = 60;
  const MIN_STATUS_REFRESH_TIMEOUT_SECONDS = 10;
  const MAX_STATUS_REFRESH_TIMEOUT_SECONDS = 120;
  const MIN_CHANNEL_SCAN_FRESHNESS_SECONDS = 30;
  const MAX_CHANNEL_SCAN_FRESHNESS_SECONDS = 600;
  const MIN_RECOVERY_RETRY_BASE_SECONDS = 5;
  const MAX_RECOVERY_RETRY_BASE_SECONDS = 120;
  const MIN_RECOVERY_RETRY_MAX_SECONDS = 60;
  const MAX_RECOVERY_RETRY_MAX_SECONDS = 1800;
  const MIN_ABSENT_STATION_RETRY_LIMIT = 1;
  const MAX_ABSENT_STATION_RETRY_LIMIT = 20;
  const MIN_BLUETOOTH_INIT_RETRY_SECONDS = 1;
  const MAX_BLUETOOTH_INIT_RETRY_SECONDS = 30;

  let {
    adapters,
    loading,
    loadError,
    autoSleep,
    autoSleepError = null,
    autoSleepBusy = false,
    scanOnStartup,
    scanOnStartupError = null,
    scanOnStartupBusy = false,
    scanDurationSeconds,
    scanDurationError = null,
    scanDurationBusy = false,
    statusPollingEnabled,
    statusPollingEnabledError = null,
    statusPollingEnabledBusy = false,
    bulkPowerTimeoutSeconds,
    bulkPowerTimeoutError = null,
    bulkPowerTimeoutBusy = false,
    statusPollIntervalSeconds,
    statusPollIntervalError = null,
    statusPollIntervalBusy = false,
    stationOperationTimeoutSeconds,
    stationOperationTimeoutError = null,
    stationOperationTimeoutBusy = false,
    powerConfirmAttemptsOn,
    powerConfirmAttemptsOnError = null,
    powerConfirmAttemptsOnBusy = false,
    powerConfirmAttemptsOff,
    powerConfirmAttemptsOffError = null,
    powerConfirmAttemptsOffBusy = false,
    powerConfirmPollIntervalMs,
    powerConfirmPollIntervalError = null,
    powerConfirmPollIntervalBusy = false,
    bootFallbackSeconds,
    bootFallbackError = null,
    bootFallbackBusy = false,
    sleepFinalWriteTimeoutSeconds,
    sleepFinalWriteTimeoutError = null,
    sleepFinalWriteTimeoutBusy = false,
    sleepPrepareGapMs,
    sleepPrepareGapError = null,
    sleepPrepareGapBusy = false,
    discoveryAttempts,
    discoveryAttemptsError = null,
    discoveryAttemptsBusy = false,
    discoveryRetryDelayMs,
    discoveryRetryDelayError = null,
    discoveryRetryDelayBusy = false,
    apiListenAddress,
    apiListenAddressError = null,
    apiListenAddressBusy = false,
    powerWriteAttempts,
    powerWriteAttemptsError = null,
    powerWriteAttemptsBusy = false,
    operationRetryDelayMs,
    operationRetryDelayError = null,
    operationRetryDelayBusy = false,
    identifyAttempts,
    identifyAttemptsError = null,
    identifyAttemptsBusy = false,
    confirmReconnectThreshold,
    confirmReconnectThresholdError = null,
    confirmReconnectThresholdBusy = false,
    confirmReconnectDelayMs,
    confirmReconnectDelayError = null,
    confirmReconnectDelayBusy = false,
    channelConfirmAttempts,
    channelConfirmAttemptsError = null,
    channelConfirmAttemptsBusy = false,
    channelConfirmIntervalMs,
    channelConfirmIntervalError = null,
    channelConfirmIntervalBusy = false,
    presenceMissThreshold,
    presenceMissThresholdError = null,
    presenceMissThresholdBusy = false,
    initialReadTimeoutSeconds,
    initialReadTimeoutError = null,
    initialReadTimeoutBusy = false,
    scanReadPhaseTimeoutSeconds,
    scanReadPhaseTimeoutError = null,
    scanReadPhaseTimeoutBusy = false,
    statusReadTimeoutSeconds,
    statusReadTimeoutError = null,
    statusReadTimeoutBusy = false,
    statusRefreshTimeoutSeconds,
    statusRefreshTimeoutError = null,
    statusRefreshTimeoutBusy = false,
    channelScanFreshnessSeconds,
    channelScanFreshnessError = null,
    channelScanFreshnessBusy = false,
    recoveryRetryBaseSeconds,
    recoveryRetryBaseError = null,
    recoveryRetryBaseBusy = false,
    recoveryRetryMaxSeconds,
    recoveryRetryMaxError = null,
    recoveryRetryMaxBusy = false,
    absentStationRetryLimit,
    absentStationRetryLimitError = null,
    absentStationRetryLimitBusy = false,
    bluetoothInitRetrySeconds,
    bluetoothInitRetryError = null,
    bluetoothInitRetryBusy = false,
    languagePreference = 'system',
    languageBusy = false,
    inactive = false,
    onClose,
    onRefresh,
    onAutoSleepChange,
    onAutoSleepRetry,
    onScanOnStartupChange,
    onScanOnStartupRetry,
    onScanDurationChange,
    onScanDurationRetry,
    onStatusPollingEnabledChange,
    onStatusPollingEnabledRetry,
    onBulkPowerTimeoutChange,
    onBulkPowerTimeoutRetry,
    onStatusPollIntervalChange,
    onStatusPollIntervalRetry,
    onStationOperationTimeoutChange,
    onStationOperationTimeoutRetry,
    onPowerConfirmAttemptsOnChange,
    onPowerConfirmAttemptsOnRetry,
    onPowerConfirmAttemptsOffChange,
    onPowerConfirmAttemptsOffRetry,
    onPowerConfirmPollIntervalChange,
    onPowerConfirmPollIntervalRetry,
    onBootFallbackChange,
    onBootFallbackRetry,
    onSleepFinalWriteTimeoutChange,
    onSleepFinalWriteTimeoutRetry,
    onSleepPrepareGapChange,
    onSleepPrepareGapRetry,
    onDiscoveryAttemptsChange,
    onDiscoveryAttemptsRetry,
    onDiscoveryRetryDelayChange,
    onDiscoveryRetryDelayRetry,
    onAPIListenAddressChange,
    onAPIListenAddressRetry,
    onPowerWriteAttemptsChange,
    onPowerWriteAttemptsRetry,
    onOperationRetryDelayChange,
    onOperationRetryDelayRetry,
    onIdentifyAttemptsChange,
    onIdentifyAttemptsRetry,
    onConfirmReconnectThresholdChange,
    onConfirmReconnectThresholdRetry,
    onConfirmReconnectDelayChange,
    onConfirmReconnectDelayRetry,
    onChannelConfirmAttemptsChange,
    onChannelConfirmAttemptsRetry,
    onChannelConfirmIntervalChange,
    onChannelConfirmIntervalRetry,
    onPresenceMissThresholdChange,
    onPresenceMissThresholdRetry,
    onInitialReadTimeoutChange,
    onInitialReadTimeoutRetry,
    onScanReadPhaseTimeoutChange,
    onScanReadPhaseTimeoutRetry,
    onStatusReadTimeoutChange,
    onStatusReadTimeoutRetry,
    onStatusRefreshTimeoutChange,
    onStatusRefreshTimeoutRetry,
    onChannelScanFreshnessChange,
    onChannelScanFreshnessRetry,
    onRecoveryRetryBaseChange,
    onRecoveryRetryBaseRetry,
    onRecoveryRetryMaxChange,
    onRecoveryRetryMaxRetry,
    onAbsentStationRetryLimitChange,
    onAbsentStationRetryLimitRetry,
    onBluetoothInitRetryChange,
    onBluetoothInitRetryRetry,
    onLanguageChange = () => {}
  }: {
    adapters: bluetooth.AdapterInfo[];
    loading: boolean;
    loadError: string | null;
    autoSleep: autosleep.Settings | null;
    autoSleepError?: string | null;
    autoSleepBusy?: boolean;
    scanOnStartup: boolean | null;
    scanOnStartupError?: string | null;
    scanOnStartupBusy?: boolean;
    scanDurationSeconds: number | null;
    scanDurationError?: string | null;
    scanDurationBusy?: boolean;
    statusPollingEnabled: boolean | null;
    statusPollingEnabledError?: string | null;
    statusPollingEnabledBusy?: boolean;
    bulkPowerTimeoutSeconds: number | null;
    bulkPowerTimeoutError?: string | null;
    bulkPowerTimeoutBusy?: boolean;
    statusPollIntervalSeconds: number | null;
    statusPollIntervalError?: string | null;
    statusPollIntervalBusy?: boolean;
    stationOperationTimeoutSeconds: number | null;
    stationOperationTimeoutError?: string | null;
    stationOperationTimeoutBusy?: boolean;
    powerConfirmAttemptsOn: number | null;
    powerConfirmAttemptsOnError?: string | null;
    powerConfirmAttemptsOnBusy?: boolean;
    powerConfirmAttemptsOff: number | null;
    powerConfirmAttemptsOffError?: string | null;
    powerConfirmAttemptsOffBusy?: boolean;
    powerConfirmPollIntervalMs: number | null;
    powerConfirmPollIntervalError?: string | null;
    powerConfirmPollIntervalBusy?: boolean;
    bootFallbackSeconds: number | null;
    bootFallbackError?: string | null;
    bootFallbackBusy?: boolean;
    sleepFinalWriteTimeoutSeconds: number | null;
    sleepFinalWriteTimeoutError?: string | null;
    sleepFinalWriteTimeoutBusy?: boolean;
    sleepPrepareGapMs: number | null;
    sleepPrepareGapError?: string | null;
    sleepPrepareGapBusy?: boolean;
    discoveryAttempts: number | null;
    discoveryAttemptsError?: string | null;
    discoveryAttemptsBusy?: boolean;
    discoveryRetryDelayMs: number | null;
    discoveryRetryDelayError?: string | null;
    discoveryRetryDelayBusy?: boolean;
    apiListenAddress: string | null;
    apiListenAddressError?: string | null;
    apiListenAddressBusy?: boolean;
    powerWriteAttempts: number | null;
    powerWriteAttemptsError?: string | null;
    powerWriteAttemptsBusy?: boolean;
    operationRetryDelayMs: number | null;
    operationRetryDelayError?: string | null;
    operationRetryDelayBusy?: boolean;
    identifyAttempts: number | null;
    identifyAttemptsError?: string | null;
    identifyAttemptsBusy?: boolean;
    confirmReconnectThreshold: number | null;
    confirmReconnectThresholdError?: string | null;
    confirmReconnectThresholdBusy?: boolean;
    confirmReconnectDelayMs: number | null;
    confirmReconnectDelayError?: string | null;
    confirmReconnectDelayBusy?: boolean;
    channelConfirmAttempts: number | null;
    channelConfirmAttemptsError?: string | null;
    channelConfirmAttemptsBusy?: boolean;
    channelConfirmIntervalMs: number | null;
    channelConfirmIntervalError?: string | null;
    channelConfirmIntervalBusy?: boolean;
    presenceMissThreshold: number | null;
    presenceMissThresholdError?: string | null;
    presenceMissThresholdBusy?: boolean;
    initialReadTimeoutSeconds: number | null;
    initialReadTimeoutError?: string | null;
    initialReadTimeoutBusy?: boolean;
    scanReadPhaseTimeoutSeconds: number | null;
    scanReadPhaseTimeoutError?: string | null;
    scanReadPhaseTimeoutBusy?: boolean;
    statusReadTimeoutSeconds: number | null;
    statusReadTimeoutError?: string | null;
    statusReadTimeoutBusy?: boolean;
    statusRefreshTimeoutSeconds: number | null;
    statusRefreshTimeoutError?: string | null;
    statusRefreshTimeoutBusy?: boolean;
    channelScanFreshnessSeconds: number | null;
    channelScanFreshnessError?: string | null;
    channelScanFreshnessBusy?: boolean;
    recoveryRetryBaseSeconds: number | null;
    recoveryRetryBaseError?: string | null;
    recoveryRetryBaseBusy?: boolean;
    recoveryRetryMaxSeconds: number | null;
    recoveryRetryMaxError?: string | null;
    recoveryRetryMaxBusy?: boolean;
    absentStationRetryLimit: number | null;
    absentStationRetryLimitError?: string | null;
    absentStationRetryLimitBusy?: boolean;
    bluetoothInitRetrySeconds: number | null;
    bluetoothInitRetryError?: string | null;
    bluetoothInitRetryBusy?: boolean;
    languagePreference?: LanguagePreference;
    languageBusy?: boolean;
    inactive?: boolean;
    onClose: () => void;
    onRefresh: () => void;
    onAutoSleepChange: (settings: autosleep.Settings) => void;
    onAutoSleepRetry?: () => void;
    onScanOnStartupChange: (enabled: boolean) => void;
    onScanOnStartupRetry?: () => void;
    onScanDurationChange: (durationSeconds: number) => void;
    onScanDurationRetry?: () => void;
    onStatusPollingEnabledChange: (enabled: boolean) => void;
    onStatusPollingEnabledRetry?: () => void;
    onBulkPowerTimeoutChange: (timeoutSeconds: number) => void;
    onBulkPowerTimeoutRetry?: () => void;
    onStatusPollIntervalChange: (intervalSeconds: number) => void;
    onStatusPollIntervalRetry?: () => void;
    onStationOperationTimeoutChange: (timeoutSeconds: number) => void;
    onStationOperationTimeoutRetry?: () => void;
    onPowerConfirmAttemptsOnChange: (attempts: number) => void;
    onPowerConfirmAttemptsOnRetry?: () => void;
    onPowerConfirmAttemptsOffChange: (attempts: number) => void;
    onPowerConfirmAttemptsOffRetry?: () => void;
    onPowerConfirmPollIntervalChange: (intervalMs: number) => void;
    onPowerConfirmPollIntervalRetry?: () => void;
    onBootFallbackChange: (fallbackSeconds: number) => void;
    onBootFallbackRetry?: () => void;
    onSleepFinalWriteTimeoutChange: (timeoutSeconds: number) => void;
    onSleepFinalWriteTimeoutRetry?: () => void;
    onSleepPrepareGapChange: (gapMs: number) => void;
    onSleepPrepareGapRetry?: () => void;
    onDiscoveryAttemptsChange: (attempts: number) => void;
    onDiscoveryAttemptsRetry?: () => void;
    onDiscoveryRetryDelayChange: (delayMs: number) => void;
    onDiscoveryRetryDelayRetry?: () => void;
    onAPIListenAddressChange: (address: string) => void;
    onAPIListenAddressRetry?: () => void;
    onPowerWriteAttemptsChange: (attempts: number) => void;
    onPowerWriteAttemptsRetry?: () => void;
    onOperationRetryDelayChange: (delayMs: number) => void;
    onOperationRetryDelayRetry?: () => void;
    onIdentifyAttemptsChange: (attempts: number) => void;
    onIdentifyAttemptsRetry?: () => void;
    onConfirmReconnectThresholdChange: (threshold: number) => void;
    onConfirmReconnectThresholdRetry?: () => void;
    onConfirmReconnectDelayChange: (delayMs: number) => void;
    onConfirmReconnectDelayRetry?: () => void;
    onChannelConfirmAttemptsChange: (attempts: number) => void;
    onChannelConfirmAttemptsRetry?: () => void;
    onChannelConfirmIntervalChange: (intervalMs: number) => void;
    onChannelConfirmIntervalRetry?: () => void;
    onPresenceMissThresholdChange: (threshold: number) => void;
    onPresenceMissThresholdRetry?: () => void;
    onInitialReadTimeoutChange: (timeoutSeconds: number) => void;
    onInitialReadTimeoutRetry?: () => void;
    onScanReadPhaseTimeoutChange: (timeoutSeconds: number) => void;
    onScanReadPhaseTimeoutRetry?: () => void;
    onStatusReadTimeoutChange: (timeoutSeconds: number) => void;
    onStatusReadTimeoutRetry?: () => void;
    onStatusRefreshTimeoutChange: (timeoutSeconds: number) => void;
    onStatusRefreshTimeoutRetry?: () => void;
    onChannelScanFreshnessChange: (freshnessSeconds: number) => void;
    onChannelScanFreshnessRetry?: () => void;
    onRecoveryRetryBaseChange: (baseSeconds: number) => void;
    onRecoveryRetryBaseRetry?: () => void;
    onRecoveryRetryMaxChange: (maxSeconds: number) => void;
    onRecoveryRetryMaxRetry?: () => void;
    onAbsentStationRetryLimitChange: (limit: number) => void;
    onAbsentStationRetryLimitRetry?: () => void;
    onBluetoothInitRetryChange: (retrySeconds: number) => void;
    onBluetoothInitRetryRetry?: () => void;
    onLanguageChange?: (language: LanguagePreference) => void;
  } = $props();

  // The delay input keeps a draft copy so typing never saves; only commit
  // (blur/Enter) pushes a change, matching the rest of the drawer. Draft
  // syncing only overwrites input the user has not touched: a save failure
  // rollback must not clobber a value the user is mid-way through typing.
  let delayDraft = $state<string | number | null>('');
  let knownDelaySeconds: number | null = null;
  $effect(() => {
    const current = autoSleep ? autoSleep.delaySeconds : null;
    if (current === null || current === knownDelaySeconds) return;
    if (knownDelaySeconds === null || String(delayDraft ?? '') === String(Math.round(knownDelaySeconds / 60))) {
      delayDraft = String(Math.round(current / 60));
    }
    knownDelaySeconds = current;
  });

  let bulkTimeoutDraft = $state<string | number | null>('');
  let knownBulkTimeout: number | null = null;
  $effect(() => {
    const current = bulkPowerTimeoutSeconds;
    if (current === null || current === knownBulkTimeout) return;
    if (knownBulkTimeout === null || String(bulkTimeoutDraft ?? '') === String(knownBulkTimeout)) {
      bulkTimeoutDraft = String(current);
    }
    knownBulkTimeout = current;
  });

  let statusPollIntervalDraft = $state<string | number | null>('');
  let knownStatusPollInterval: number | null = null;
  $effect(() => {
    const current = statusPollIntervalSeconds;
    if (current === null || current === knownStatusPollInterval) return;
    if (knownStatusPollInterval === null || String(statusPollIntervalDraft ?? '') === String(knownStatusPollInterval)) {
      statusPollIntervalDraft = String(current);
    }
    knownStatusPollInterval = current;
  });

  let scanDurationDraft = $state<string | number | null>('');
  let knownScanDuration: number | null = null;
  $effect(() => {
    const current = scanDurationSeconds;
    if (current === null || current === knownScanDuration) return;
    if (knownScanDuration === null || String(scanDurationDraft ?? '') === String(knownScanDuration)) {
      scanDurationDraft = String(current);
    }
    knownScanDuration = current;
  });

  let stationTimeoutDraft = $state<string | number | null>('');
  let knownStationTimeout: number | null = null;
  $effect(() => {
    const current = stationOperationTimeoutSeconds;
    if (current === null || current === knownStationTimeout) return;
    if (knownStationTimeout === null || String(stationTimeoutDraft ?? '') === String(knownStationTimeout)) {
      stationTimeoutDraft = String(current);
    }
    knownStationTimeout = current;
  });

  let confirmAttemptsOnDraft = $state<string | number | null>('');
  let knownConfirmAttemptsOn: number | null = null;
  $effect(() => {
    const current = powerConfirmAttemptsOn;
    if (current === null || current === knownConfirmAttemptsOn) return;
    if (knownConfirmAttemptsOn === null || String(confirmAttemptsOnDraft ?? '') === String(knownConfirmAttemptsOn)) {
      confirmAttemptsOnDraft = String(current);
    }
    knownConfirmAttemptsOn = current;
  });

  let confirmAttemptsOffDraft = $state<string | number | null>('');
  let knownConfirmAttemptsOff: number | null = null;
  $effect(() => {
    const current = powerConfirmAttemptsOff;
    if (current === null || current === knownConfirmAttemptsOff) return;
    if (knownConfirmAttemptsOff === null || String(confirmAttemptsOffDraft ?? '') === String(knownConfirmAttemptsOff)) {
      confirmAttemptsOffDraft = String(current);
    }
    knownConfirmAttemptsOff = current;
  });

  let confirmPollIntervalDraft = $state<string | number | null>('');
  let knownConfirmPollInterval: number | null = null;
  $effect(() => {
    const current = powerConfirmPollIntervalMs;
    if (current === null || current === knownConfirmPollInterval) return;
    if (knownConfirmPollInterval === null || String(confirmPollIntervalDraft ?? '') === String(knownConfirmPollInterval)) {
      confirmPollIntervalDraft = String(current);
    }
    knownConfirmPollInterval = current;
  });

  let bootFallbackDraft = $state<string | number | null>('');
  let knownBootFallback: number | null = null;
  $effect(() => {
    const current = bootFallbackSeconds;
    if (current === null || current === knownBootFallback) return;
    if (knownBootFallback === null || String(bootFallbackDraft ?? '') === String(knownBootFallback)) {
      bootFallbackDraft = String(current);
    }
    knownBootFallback = current;
  });

  let sleepFinalWriteDraft = $state<string | number | null>('');
  let knownSleepFinalWrite: number | null = null;
  $effect(() => {
    const current = sleepFinalWriteTimeoutSeconds;
    if (current === null || current === knownSleepFinalWrite) return;
    if (knownSleepFinalWrite === null || String(sleepFinalWriteDraft ?? '') === String(knownSleepFinalWrite)) {
      sleepFinalWriteDraft = String(current);
    }
    knownSleepFinalWrite = current;
  });

  let sleepPrepareGapDraft = $state<string | number | null>('');
  let knownSleepPrepareGap: number | null = null;
  $effect(() => {
    const current = sleepPrepareGapMs;
    if (current === null || current === knownSleepPrepareGap) return;
    if (knownSleepPrepareGap === null || String(sleepPrepareGapDraft ?? '') === String(knownSleepPrepareGap)) {
      sleepPrepareGapDraft = String(current);
    }
    knownSleepPrepareGap = current;
  });

  let discoveryAttemptsDraft = $state<string | number | null>('');
  let knownDiscoveryAttempts: number | null = null;
  $effect(() => {
    const current = discoveryAttempts;
    if (current === null || current === knownDiscoveryAttempts) return;
    if (knownDiscoveryAttempts === null || String(discoveryAttemptsDraft ?? '') === String(knownDiscoveryAttempts)) {
      discoveryAttemptsDraft = String(current);
    }
    knownDiscoveryAttempts = current;
  });

  let discoveryRetryDelayDraft = $state<string | number | null>('');
  let knownDiscoveryRetryDelay: number | null = null;
  $effect(() => {
    const current = discoveryRetryDelayMs;
    if (current === null || current === knownDiscoveryRetryDelay) return;
    if (knownDiscoveryRetryDelay === null || String(discoveryRetryDelayDraft ?? '') === String(knownDiscoveryRetryDelay)) {
      discoveryRetryDelayDraft = String(current);
    }
    knownDiscoveryRetryDelay = current;
  });

  let apiAddressDraft = $state('');
  let knownAPIAddress: string | null = null;
  $effect(() => {
    const current = apiListenAddress;
    if (current === null || current === knownAPIAddress) return;
    if (knownAPIAddress === null || apiAddressDraft === knownAPIAddress) {
      apiAddressDraft = current;
    }
    knownAPIAddress = current;
  });

  // Shared draft bookkeeping for the advanced numeric settings: keep the input
  // in sync with the loaded value while never clobbering mid-edit input, and
  // clamp on commit.
  type NumericDraftValue = string | number | null;
  function numericDraft(getValue: () => number | null) {
    let draft = $state<NumericDraftValue>('');
    let known: number | null = null;
    $effect(() => {
      const current = getValue();
      if (current === null || current === known) return;
      if (known === null || String(draft ?? '') === String(known)) {
        draft = String(current);
      }
      known = current;
    });
    return {
      get draft(): NumericDraftValue {
        return draft;
      },
      set draft(value: NumericDraftValue) {
        draft = value;
      },
      commit(min: number, max: number, apply: (value: number) => void) {
        const current = getValue();
        if (current === null) return;
        const value = clampedDraft(draft, current, min, max);
        draft = String(value);
        if (value !== current) apply(value);
      }
    };
  }

  const powerWriteAttemptsDraft = numericDraft(() => powerWriteAttempts);
  const operationRetryDelayDraft = numericDraft(() => operationRetryDelayMs);
  const identifyAttemptsDraft = numericDraft(() => identifyAttempts);
  const confirmReconnectThresholdDraft = numericDraft(() => confirmReconnectThreshold);
  const confirmReconnectDelayDraft = numericDraft(() => confirmReconnectDelayMs);
  const channelConfirmAttemptsDraft = numericDraft(() => channelConfirmAttempts);
  const channelConfirmIntervalDraft = numericDraft(() => channelConfirmIntervalMs);
  const presenceMissThresholdDraft = numericDraft(() => presenceMissThreshold);
  const initialReadTimeoutDraft = numericDraft(() => initialReadTimeoutSeconds);
  const scanReadPhaseTimeoutDraft = numericDraft(() => scanReadPhaseTimeoutSeconds);
  const statusReadTimeoutDraft = numericDraft(() => statusReadTimeoutSeconds);
  const statusRefreshTimeoutDraft = numericDraft(() => statusRefreshTimeoutSeconds);
  const channelScanFreshnessDraft = numericDraft(() => channelScanFreshnessSeconds);
  const recoveryRetryBaseDraft = numericDraft(() => recoveryRetryBaseSeconds);
  const recoveryRetryMaxDraft = numericDraft(() => recoveryRetryMaxSeconds);
  const absentStationRetryLimitDraft = numericDraft(() => absentStationRetryLimit);
  const bluetoothInitRetryDraft = numericDraft(() => bluetoothInitRetrySeconds);

  // A cleared input must restore the current value instead of committing the
  // range boundary. Number-input bindings hand back null for a cleared field
  // (and Number('') is 0), which would otherwise clamp to MIN and silently
  // persist an unintended setting. A typed number — including 0 — is real input
  // and is clamped to the range instead.
  function clampedDraft(draft: string | number | null, fallback: number, min: number, max: number): number {
    const text = String(draft ?? '').trim();
    if (text === '') return fallback;
    const parsed = Number(text);
    if (!Number.isFinite(parsed)) return fallback;
    return Math.min(max, Math.max(min, Math.round(parsed)));
  }

  function commitDelay() {
    if (!autoSleep) return;
    const text = String(delayDraft ?? '').trim();
    const parsed = Number(text);
    if (text === '' || !Number.isFinite(parsed)) {
      // A cleared or non-numeric field restores the shown value without a
      // commit; committing here would round-trip a hand-edited delay that is
      // not a whole number of minutes into a different setting.
      delayDraft = String(Math.round(autoSleep.delaySeconds / 60));
      return;
    }
    const minutes = Math.min(MAX_DELAY_MINUTES, Math.max(MIN_DELAY_MINUTES, Math.round(parsed)));
    delayDraft = String(minutes);
    const delaySeconds = minutes * 60;
    if (delaySeconds !== autoSleep.delaySeconds) {
      onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, delaySeconds }));
    }
  }

  function commitBulkTimeout() {
    if (bulkPowerTimeoutSeconds === null) return;
    const seconds = clampedDraft(bulkTimeoutDraft, bulkPowerTimeoutSeconds, MIN_BULK_TIMEOUT_SECONDS, MAX_BULK_TIMEOUT_SECONDS);
    bulkTimeoutDraft = String(seconds);
    if (seconds !== bulkPowerTimeoutSeconds) onBulkPowerTimeoutChange(seconds);
  }

  function commitStatusPollInterval() {
    if (statusPollIntervalSeconds === null) return;
    const seconds = clampedDraft(statusPollIntervalDraft, statusPollIntervalSeconds, MIN_STATUS_POLL_INTERVAL_SECONDS, MAX_STATUS_POLL_INTERVAL_SECONDS);
    statusPollIntervalDraft = String(seconds);
    if (seconds !== statusPollIntervalSeconds) onStatusPollIntervalChange(seconds);
  }

  function commitScanDuration() {
    if (scanDurationSeconds === null) return;
    const seconds = clampedDraft(scanDurationDraft, scanDurationSeconds, MIN_SCAN_DURATION_SECONDS, MAX_SCAN_DURATION_SECONDS);
    scanDurationDraft = String(seconds);
    if (seconds !== scanDurationSeconds) onScanDurationChange(seconds);
  }

  function commitStationTimeout() {
    if (stationOperationTimeoutSeconds === null) return;
    const seconds = clampedDraft(stationTimeoutDraft, stationOperationTimeoutSeconds, MIN_STATION_TIMEOUT_SECONDS, MAX_STATION_TIMEOUT_SECONDS);
    stationTimeoutDraft = String(seconds);
    if (seconds !== stationOperationTimeoutSeconds) onStationOperationTimeoutChange(seconds);
  }

  function commitConfirmAttemptsOn() {
    if (powerConfirmAttemptsOn === null) return;
    const attempts = clampedDraft(confirmAttemptsOnDraft, powerConfirmAttemptsOn, MIN_POWER_CONFIRM_ATTEMPTS, MAX_POWER_CONFIRM_ATTEMPTS);
    confirmAttemptsOnDraft = String(attempts);
    if (attempts !== powerConfirmAttemptsOn) onPowerConfirmAttemptsOnChange(attempts);
  }

  function commitConfirmAttemptsOff() {
    if (powerConfirmAttemptsOff === null) return;
    const attempts = clampedDraft(confirmAttemptsOffDraft, powerConfirmAttemptsOff, MIN_POWER_CONFIRM_ATTEMPTS, MAX_POWER_CONFIRM_ATTEMPTS);
    confirmAttemptsOffDraft = String(attempts);
    if (attempts !== powerConfirmAttemptsOff) onPowerConfirmAttemptsOffChange(attempts);
  }

  function commitConfirmPollInterval() {
    if (powerConfirmPollIntervalMs === null) return;
    const intervalMs = clampedDraft(confirmPollIntervalDraft, powerConfirmPollIntervalMs, MIN_POWER_CONFIRM_POLL_MS, MAX_POWER_CONFIRM_POLL_MS);
    confirmPollIntervalDraft = String(intervalMs);
    if (intervalMs !== powerConfirmPollIntervalMs) onPowerConfirmPollIntervalChange(intervalMs);
  }

  function commitBootFallback() {
    if (bootFallbackSeconds === null) return;
    const seconds = clampedDraft(bootFallbackDraft, bootFallbackSeconds, MIN_BOOT_FALLBACK_SECONDS, MAX_BOOT_FALLBACK_SECONDS);
    bootFallbackDraft = String(seconds);
    if (seconds !== bootFallbackSeconds) onBootFallbackChange(seconds);
  }

  function commitSleepFinalWrite() {
    if (sleepFinalWriteTimeoutSeconds === null) return;
    const seconds = clampedDraft(sleepFinalWriteDraft, sleepFinalWriteTimeoutSeconds, MIN_SLEEP_FINAL_WRITE_SECONDS, MAX_SLEEP_FINAL_WRITE_SECONDS);
    sleepFinalWriteDraft = String(seconds);
    if (seconds !== sleepFinalWriteTimeoutSeconds) onSleepFinalWriteTimeoutChange(seconds);
  }

  function commitSleepPrepareGap() {
    if (sleepPrepareGapMs === null) return;
    const text = String(sleepPrepareGapDraft ?? '').trim();
    const parsed = Number(text);
    // Zero disables the gap and is valid, so only an empty or non-numeric
    // input falls back to the current value here.
    const gapMs = text !== '' && Number.isFinite(parsed)
      ? Math.min(MAX_SLEEP_PREPARE_GAP_MS, Math.max(MIN_SLEEP_PREPARE_GAP_MS, Math.round(parsed)))
      : sleepPrepareGapMs;
    sleepPrepareGapDraft = String(gapMs);
    if (gapMs !== sleepPrepareGapMs) onSleepPrepareGapChange(gapMs);
  }

  function commitDiscoveryAttempts() {
    if (discoveryAttempts === null) return;
    const attempts = clampedDraft(discoveryAttemptsDraft, discoveryAttempts, MIN_DISCOVERY_ATTEMPTS, MAX_DISCOVERY_ATTEMPTS);
    discoveryAttemptsDraft = String(attempts);
    if (attempts !== discoveryAttempts) onDiscoveryAttemptsChange(attempts);
  }

  function commitDiscoveryRetryDelay() {
    if (discoveryRetryDelayMs === null) return;
    const delayMs = clampedDraft(discoveryRetryDelayDraft, discoveryRetryDelayMs, MIN_DISCOVERY_RETRY_DELAY_MS, MAX_DISCOVERY_RETRY_DELAY_MS);
    discoveryRetryDelayDraft = String(delayMs);
    if (delayMs !== discoveryRetryDelayMs) onDiscoveryRetryDelayChange(delayMs);
  }

  function commitAPIListenAddress() {
    if (apiListenAddress === null) return;
    const address = apiAddressDraft.trim();
    if (address === '') {
      apiAddressDraft = apiListenAddress;
      return;
    }
    if (address !== apiListenAddress) onAPIListenAddressChange(address);
  }

  function commitPowerWriteAttempts() {
    powerWriteAttemptsDraft.commit(MIN_POWER_WRITE_ATTEMPTS, MAX_POWER_WRITE_ATTEMPTS, onPowerWriteAttemptsChange);
  }

  function commitOperationRetryDelay() {
    operationRetryDelayDraft.commit(MIN_OPERATION_RETRY_DELAY_MS, MAX_OPERATION_RETRY_DELAY_MS, onOperationRetryDelayChange);
  }

  function commitIdentifyAttempts() {
    identifyAttemptsDraft.commit(MIN_IDENTIFY_ATTEMPTS, MAX_IDENTIFY_ATTEMPTS, onIdentifyAttemptsChange);
  }

  function commitConfirmReconnectThreshold() {
    confirmReconnectThresholdDraft.commit(MIN_CONFIRM_RECONNECT_THRESHOLD, MAX_CONFIRM_RECONNECT_THRESHOLD, onConfirmReconnectThresholdChange);
  }

  function commitConfirmReconnectDelay() {
    confirmReconnectDelayDraft.commit(MIN_CONFIRM_RECONNECT_DELAY_MS, MAX_CONFIRM_RECONNECT_DELAY_MS, onConfirmReconnectDelayChange);
  }

  function commitChannelConfirmAttempts() {
    channelConfirmAttemptsDraft.commit(MIN_CHANNEL_CONFIRM_ATTEMPTS, MAX_CHANNEL_CONFIRM_ATTEMPTS, onChannelConfirmAttemptsChange);
  }

  function commitChannelConfirmInterval() {
    channelConfirmIntervalDraft.commit(MIN_CHANNEL_CONFIRM_INTERVAL_MS, MAX_CHANNEL_CONFIRM_INTERVAL_MS, onChannelConfirmIntervalChange);
  }

  function commitPresenceMissThreshold() {
    presenceMissThresholdDraft.commit(MIN_PRESENCE_MISS_THRESHOLD, MAX_PRESENCE_MISS_THRESHOLD, onPresenceMissThresholdChange);
  }

  function commitInitialReadTimeout() {
    initialReadTimeoutDraft.commit(MIN_INITIAL_READ_TIMEOUT_SECONDS, MAX_INITIAL_READ_TIMEOUT_SECONDS, onInitialReadTimeoutChange);
  }

  function commitScanReadPhaseTimeout() {
    scanReadPhaseTimeoutDraft.commit(MIN_SCAN_READ_PHASE_TIMEOUT_SECONDS, MAX_SCAN_READ_PHASE_TIMEOUT_SECONDS, onScanReadPhaseTimeoutChange);
  }

  function commitStatusReadTimeout() {
    statusReadTimeoutDraft.commit(MIN_STATUS_READ_TIMEOUT_SECONDS, MAX_STATUS_READ_TIMEOUT_SECONDS, onStatusReadTimeoutChange);
  }

  function commitStatusRefreshTimeout() {
    statusRefreshTimeoutDraft.commit(MIN_STATUS_REFRESH_TIMEOUT_SECONDS, MAX_STATUS_REFRESH_TIMEOUT_SECONDS, onStatusRefreshTimeoutChange);
  }

  function commitChannelScanFreshness() {
    channelScanFreshnessDraft.commit(MIN_CHANNEL_SCAN_FRESHNESS_SECONDS, MAX_CHANNEL_SCAN_FRESHNESS_SECONDS, onChannelScanFreshnessChange);
  }

  function commitRecoveryRetryBase() {
    recoveryRetryBaseDraft.commit(MIN_RECOVERY_RETRY_BASE_SECONDS, MAX_RECOVERY_RETRY_BASE_SECONDS, onRecoveryRetryBaseChange);
  }

  function commitRecoveryRetryMax() {
    recoveryRetryMaxDraft.commit(MIN_RECOVERY_RETRY_MAX_SECONDS, MAX_RECOVERY_RETRY_MAX_SECONDS, onRecoveryRetryMaxChange);
  }

  function commitAbsentStationRetryLimit() {
    absentStationRetryLimitDraft.commit(MIN_ABSENT_STATION_RETRY_LIMIT, MAX_ABSENT_STATION_RETRY_LIMIT, onAbsentStationRetryLimitChange);
  }

  function commitBluetoothInitRetry() {
    bluetoothInitRetryDraft.commit(MIN_BLUETOOTH_INIT_RETRY_SECONDS, MAX_BLUETOOTH_INIT_RETRY_SECONDS, onBluetoothInitRetryChange);
  }
</script>

<div
  class="drawer"
  role="dialog"
  aria-modal={inactive ? undefined : 'true'}
  aria-hidden={inactive ? 'true' : undefined}
  inert={inactive}
  aria-label={t('Settings')}
  tabindex="-1"
  use:focusTrap
  in:fly={dur({ x: 64, duration: 320, easing: cubicOut })}
  out:fly={dur({ x: 64, duration: 180 })}
>
  <div class="drawer-head">
    <div>
      <small>{t('Settings')}</small>
      <div class="drawer-title"><h2>{t('Preferences')}</h2></div>
    </div>
    <button type="button" class="icon-btn" title={t('Close')} aria-label={t('Close settings')} onclick={onClose}><X size={18} /></button>
  </div>

  <section>
    <h4><Languages size={12} /> {t('Language')}</h4>
    <div class="adapter-list" role="radiogroup" aria-label={t('Display language')}>
      <label class="adapter-option" class:selected={languagePreference === 'system'}>
        <input type="radio" name="display-language" value="system" checked={languagePreference === 'system'} disabled={languageBusy} onchange={() => onLanguageChange('system')} />
        <span class="adapter-name">{t('Follow system')}</span>
      </label>
      <label class="adapter-option" class:selected={languagePreference === 'en'}>
        <input type="radio" name="display-language" value="en" checked={languagePreference === 'en'} disabled={languageBusy} onchange={() => onLanguageChange('en')} />
        <span class="adapter-name">English</span>
      </label>
      <label class="adapter-option" class:selected={languagePreference === 'zh-CN'}>
        <input type="radio" name="display-language" value="zh-CN" checked={languagePreference === 'zh-CN'} disabled={languageBusy} onchange={() => onLanguageChange('zh-CN')} />
        <span class="adapter-name">简体中文</span>
      </label>
    </div>
    <p class="hint">{t('Changes apply immediately and are saved for the next start.')}</p>
  </section>

  <section>
    <h4><Radar size={12} /> {t('Scanning and refresh')}</h4>
    {#if scanOnStartup !== null}
      <label class="switch-row">
        <span class="adapter-text">
          <span class="adapter-name">{t('Scan when the application starts')}</span>
          <span class="adapter-desc">{t('Discover nearby stations automatically after startup.')}</span>
        </span>
        <input type="checkbox" class="switch" aria-label={t('Scan when the application starts')} checked={scanOnStartup} disabled={scanOnStartupBusy} onchange={() => onScanOnStartupChange(!scanOnStartup)} />
      </label>
    {:else if scanOnStartupError}
      <div class="alert danger">{scanOnStartupError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onScanOnStartupRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if scanDurationSeconds !== null}
      <div class="delay-row">
        <label for="scan-duration">{t('Bluetooth scan duration')}</label>
        <span class="delay-input">
          <input id="scan-duration" type="number" min={MIN_SCAN_DURATION_SECONDS} max={MAX_SCAN_DURATION_SECONDS} step="1" bind:value={scanDurationDraft} onchange={commitScanDuration} disabled={scanDurationBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Longer scans can find slow advertisers but take more time. Allowed range: 2–30 seconds.')}</p>
    {:else if scanDurationError}
      <div class="alert danger">{scanDurationError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onScanDurationRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if statusPollingEnabled !== null}
      <label class="switch-row">
        <span class="adapter-text">
          <span class="adapter-name">{t('Refresh station status automatically')}</span>
          <span class="adapter-desc">{t('API health continues to be monitored when station refresh is disabled.')}</span>
        </span>
        <input type="checkbox" class="switch" aria-label={t('Refresh station status automatically')} checked={statusPollingEnabled} disabled={statusPollingEnabledBusy} onchange={() => onStatusPollingEnabledChange(!statusPollingEnabled)} />
      </label>
    {:else if statusPollingEnabledError}
      <div class="alert danger">{statusPollingEnabledError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onStatusPollingEnabledRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if statusPollIntervalSeconds !== null}
      <div class="delay-row">
        <label for="status-poll-interval">{t('Status polling interval')}</label>
        <span class="delay-input">
          <input id="status-poll-interval" type="number" min={MIN_STATUS_POLL_INTERVAL_SECONDS} max={MAX_STATUS_POLL_INTERVAL_SECONDS} step="1" bind:value={statusPollIntervalDraft} onchange={commitStatusPollInterval} disabled={statusPollIntervalBusy || statusPollingEnabled === false} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Displayed state remains valid long enough for the selected interval. Allowed range: 5–300 seconds.')}</p>
    {:else if statusPollIntervalError}
      <div class="alert danger">{statusPollIntervalError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onStatusPollIntervalRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else if !scanOnStartupError && !scanDurationError && !statusPollingEnabledError}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading scan and refresh settings...')}</p>
    {/if}
  </section>

  <section>
    <h4><MoonStar size={12} /> {t('Auto sleep')}</h4>
    {#if autoSleep}
      <label class="switch-row">
        <span class="adapter-text">
          <span class="adapter-name">{t('Sleep all stations after a session ends')}</span>
          <span class="adapter-desc">{t('Scans and puts every known station to sleep')}</span>
        </span>
        <input
          type="checkbox"
          class="switch"
          aria-label={t('Enable auto sleep')}
          checked={autoSleep.enabled}
          disabled={autoSleepBusy}
          onchange={() => onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, enabled: !autoSleep.enabled }))}
        />
      </label>
      {#if autoSleep.enabled}
        <div class="adapter-list" role="radiogroup" aria-label={t('Auto sleep trigger')}>
          <label class="adapter-option" class:selected={autoSleep.target === 'steamvr'}>
            <input
              type="radio"
              name="auto-sleep-target"
              value="steamvr"
              checked={autoSleep.target === 'steamvr'}
              disabled={autoSleepBusy}
              onchange={() => onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, target: 'steamvr' }))}
            />
            <span class="adapter-text">
              <span class="adapter-name">SteamVR</span>
              <span class="adapter-desc">vrserver.exe — {t('fires while the Steam client stays open')}</span>
            </span>
          </label>
          <label class="adapter-option" class:selected={autoSleep.target === 'steam'}>
            <input
              type="radio"
              name="auto-sleep-target"
              value="steam"
              checked={autoSleep.target === 'steam'}
              disabled={autoSleepBusy}
              onchange={() => onAutoSleepChange(autosleep.Settings.createFrom({ ...autoSleep, target: 'steam' }))}
            />
            <span class="adapter-text">
              <span class="adapter-name">Steam</span>
              <span class="adapter-desc">steam.exe — {t('fires only when the Steam client fully exits')}</span>
            </span>
          </label>
        </div>
        <div class="delay-row">
          <label for="auto-sleep-delay">{t('Wait before sleeping')}</label>
          <span class="delay-input">
            <input
              id="auto-sleep-delay"
              type="number"
              min={MIN_DELAY_MINUTES}
              max={MAX_DELAY_MINUTES}
              step="1"
              bind:value={delayDraft}
              onchange={commitDelay}
              disabled={autoSleepBusy}
            />
            {t('minutes')}
          </span>
        </div>
      {/if}
      <p class="hint">
        {t('The timer starts when the watched process closes. Reopening it cancels pending or in-progress automatic sleep. Commands already completed are kept and reported; commands not yet sent are skipped. When the timer fires, a Bluetooth operation from you skips this round instead of retrying. Settings are saved and restored on the next start.')}
      </p>
    {:else if autoSleepError}
      <div class="alert danger">{autoSleepError}</div>
      <div class="drawer-actions">
        <button class="btn" onclick={() => onAutoSleepRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button>
      </div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading auto sleep settings...')}</p>
    {/if}
  </section>

  <section>
    <h4><Timer size={12} /> {t('Operation safety')}</h4>
    {#if bulkPowerTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="bulk-power-timeout">{t('Bulk power timeout')}</label>
        <span class="delay-input">
          <input id="bulk-power-timeout" type="number" min={MIN_BULK_TIMEOUT_SECONDS} max={MAX_BULK_TIMEOUT_SECONDS} step="1" bind:value={bulkTimeoutDraft} onchange={commitBulkTimeout} disabled={bulkPowerTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('A bulk power action is stopped when this total time limit is reached. Allowed range: 30–600 seconds. It must stay at least as large as the per-station operation timeout.')}</p>
    {:else if bulkPowerTimeoutError}
      <div class="alert danger">{bulkPowerTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onBulkPowerTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading operation safety settings...')}</p>
    {/if}

    {#if stationOperationTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="station-operation-timeout">{t('Station operation timeout')}</label>
        <span class="delay-input">
          <input id="station-operation-timeout" type="number" min={MIN_STATION_TIMEOUT_SECONDS} max={MAX_STATION_TIMEOUT_SECONDS} step="1" bind:value={stationTimeoutDraft} onchange={commitStationTimeout} disabled={stationOperationTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Hard limit for a single station command, including connection, verification read, write, and confirmation. Allowed range: 30–120 seconds. It cannot exceed the bulk power timeout.')}</p>
    {:else if stationOperationTimeoutError}
      <div class="alert danger">{stationOperationTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onStationOperationTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Gauge size={12} /> {t('Power operation timing')}</h4>
    {#if powerConfirmAttemptsOn !== null}
      <div class="delay-row">
        <label for="power-confirm-attempts-on">{t('Power-on confirmation attempts')}</label>
        <span class="delay-input">
          <input id="power-confirm-attempts-on" type="number" min={MIN_POWER_CONFIRM_ATTEMPTS} max={MAX_POWER_CONFIRM_ATTEMPTS} step="1" bind:value={confirmAttemptsOnDraft} onchange={commitConfirmAttemptsOn} disabled={powerConfirmAttemptsOnBusy} />
          {t('reads')}
        </span>
      </div>
      <p class="hint">{t('How many readbacks are tried after a power-on command before it is reported unconfirmed. Allowed range: 5–200.')}</p>
    {:else if powerConfirmAttemptsOnError}
      <div class="alert danger">{powerConfirmAttemptsOnError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onPowerConfirmAttemptsOnRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading power timing settings...')}</p>
    {/if}

    {#if powerConfirmAttemptsOff !== null}
      <div class="delay-row">
        <label for="power-confirm-attempts-off">{t('Sleep/standby confirmation attempts')}</label>
        <span class="delay-input">
          <input id="power-confirm-attempts-off" type="number" min={MIN_POWER_CONFIRM_ATTEMPTS} max={MAX_POWER_CONFIRM_ATTEMPTS} step="1" bind:value={confirmAttemptsOffDraft} onchange={commitConfirmAttemptsOff} disabled={powerConfirmAttemptsOffBusy} />
          {t('reads')}
        </span>
      </div>
      <p class="hint">{t('How many readbacks are tried after a sleep or standby command. Allowed range: 5–200.')}</p>
    {:else if powerConfirmAttemptsOffError}
      <div class="alert danger">{powerConfirmAttemptsOffError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onPowerConfirmAttemptsOffRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if powerConfirmPollIntervalMs !== null}
      <div class="delay-row">
        <label for="power-confirm-poll-interval">{t('Confirmation read interval')}</label>
        <span class="delay-input">
          <input id="power-confirm-poll-interval" type="number" min={MIN_POWER_CONFIRM_POLL_MS} max={MAX_POWER_CONFIRM_POLL_MS} step="10" bind:value={confirmPollIntervalDraft} onchange={commitConfirmPollInterval} disabled={powerConfirmPollIntervalBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Pause between confirmation readbacks. Allowed range: 50–2000 milliseconds.')}</p>
    {:else if powerConfirmPollIntervalError}
      <div class="alert danger">{powerConfirmPollIntervalError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onPowerConfirmPollIntervalRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if bootFallbackSeconds !== null}
      <div class="delay-row">
        <label for="boot-fallback">{t('Boot fallback window')}</label>
        <span class="delay-input">
          <input id="boot-fallback" type="number" min={MIN_BOOT_FALLBACK_SECONDS} max={MAX_BOOT_FALLBACK_SECONDS} step="1" bind:value={bootFallbackDraft} onchange={commitBootFallback} disabled={bootFallbackBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Firmware that keeps reporting boot-like values is treated as on after this observation window. Lower it for slow-booting stations, raise it for firmware that flashes boot values while awake. Allowed range: 2–60 seconds.')}</p>
    {:else if bootFallbackError}
      <div class="alert danger">{bootFallbackError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onBootFallbackRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if sleepFinalWriteTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="sleep-final-write-timeout">{t('Sleep final write timeout')}</label>
        <span class="delay-input">
          <input id="sleep-final-write-timeout" type="number" min={MIN_SLEEP_FINAL_WRITE_SECONDS} max={MAX_SLEEP_FINAL_WRITE_SECONDS} step="1" bind:value={sleepFinalWriteDraft} onchange={commitSleepFinalWrite} disabled={sleepFinalWriteTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Hard limit for the final sleep command of the paired sleep write, which completes even when the application is shutting down. Allowed range: 5–120 seconds.')}</p>
    {:else if sleepFinalWriteTimeoutError}
      <div class="alert danger">{sleepFinalWriteTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onSleepFinalWriteTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if sleepPrepareGapMs !== null}
      <div class="delay-row">
        <label for="sleep-prepare-gap">{t('Sleep prepare gap')}</label>
        <span class="delay-input">
          <input id="sleep-prepare-gap" type="number" min={MIN_SLEEP_PREPARE_GAP_MS} max={MAX_SLEEP_PREPARE_GAP_MS} step="10" bind:value={sleepPrepareGapDraft} onchange={commitSleepPrepareGap} disabled={sleepPrepareGapBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Firmware settling delay between the prepare and final writes of the paired sleep sequence. Set 0 to skip the gap. Allowed range: 0–500 milliseconds.')}</p>
    {:else if sleepPrepareGapError}
      <div class="alert danger">{sleepPrepareGapError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onSleepPrepareGapRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if powerWriteAttempts !== null}
      <div class="delay-row">
        <label for="power-write-attempts">{t('Power write attempts')}</label>
        <span class="delay-input">
          <input id="power-write-attempts" type="number" min={MIN_POWER_WRITE_ATTEMPTS} max={MAX_POWER_WRITE_ATTEMPTS} step="1" bind:value={powerWriteAttemptsDraft.draft} onchange={commitPowerWriteAttempts} disabled={powerWriteAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many times a power command write is retried, reconnecting between attempts. Allowed range: 1–5.')}</p>
    {:else if powerWriteAttemptsError}
      <div class="alert danger">{powerWriteAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onPowerWriteAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if operationRetryDelayMs !== null}
      <div class="delay-row">
        <label for="power-retry-delay">{t('Power retry delay')}</label>
        <span class="delay-input">
          <input id="power-retry-delay" type="number" min={MIN_OPERATION_RETRY_DELAY_MS} max={MAX_OPERATION_RETRY_DELAY_MS} step="100" bind:value={operationRetryDelayDraft.draft} onchange={commitOperationRetryDelay} disabled={operationRetryDelayBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Backoff between power command retries. Allowed range: 100–5000 milliseconds.')}</p>
    {:else if operationRetryDelayError}
      <div class="alert danger">{operationRetryDelayError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onOperationRetryDelayRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Cable size={12} /> {t('Connection timing')}</h4>
    {#if discoveryAttempts !== null}
      <div class="delay-row">
        <label for="discovery-attempts">{t('GATT discovery attempts')}</label>
        <span class="delay-input">
          <input id="discovery-attempts" type="number" min={MIN_DISCOVERY_ATTEMPTS} max={MAX_DISCOVERY_ATTEMPTS} step="1" bind:value={discoveryAttemptsDraft} onchange={commitDiscoveryAttempts} disabled={discoveryAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many times service discovery is retried when connecting to a station fails. Allowed range: 1–10.')}</p>
    {:else if discoveryAttemptsError}
      <div class="alert danger">{discoveryAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onDiscoveryAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading connection timing settings...')}</p>
    {/if}

    {#if discoveryRetryDelayMs !== null}
      <div class="delay-row">
        <label for="discovery-retry-delay">{t('Discovery retry delay')}</label>
        <span class="delay-input">
          <input id="discovery-retry-delay" type="number" min={MIN_DISCOVERY_RETRY_DELAY_MS} max={MAX_DISCOVERY_RETRY_DELAY_MS} step="100" bind:value={discoveryRetryDelayDraft} onchange={commitDiscoveryRetryDelay} disabled={discoveryRetryDelayBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Pause before a discovery retry after a failed connection. Allowed range: 100–5000 milliseconds.')}</p>
    {:else if discoveryRetryDelayError}
      <div class="alert danger">{discoveryRetryDelayError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onDiscoveryRetryDelayRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if identifyAttempts !== null}
      <div class="delay-row">
        <label for="identify-attempts">{t('Identify attempts')}</label>
        <span class="delay-input">
          <input id="identify-attempts" type="number" min={MIN_IDENTIFY_ATTEMPTS} max={MAX_IDENTIFY_ATTEMPTS} step="1" bind:value={identifyAttemptsDraft.draft} onchange={commitIdentifyAttempts} disabled={identifyAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many times the identify (flash) command is retried. Allowed range: 1–5.')}</p>
    {:else if identifyAttemptsError}
      <div class="alert danger">{identifyAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onIdentifyAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if confirmReconnectThreshold !== null}
      <div class="delay-row">
        <label for="confirm-reconnect-threshold">{t('Confirmation reconnect threshold')}</label>
        <span class="delay-input">
          <input id="confirm-reconnect-threshold" type="number" min={MIN_CONFIRM_RECONNECT_THRESHOLD} max={MAX_CONFIRM_RECONNECT_THRESHOLD} step="1" bind:value={confirmReconnectThresholdDraft.draft} onchange={commitConfirmReconnectThreshold} disabled={confirmReconnectThresholdBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('Consecutive confirmation read errors before a reconnect is attempted. Allowed range: 1–5.')}</p>
    {:else if confirmReconnectThresholdError}
      <div class="alert danger">{confirmReconnectThresholdError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onConfirmReconnectThresholdRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if confirmReconnectDelayMs !== null}
      <div class="delay-row">
        <label for="confirm-reconnect-delay">{t('Confirmation reconnect delay')}</label>
        <span class="delay-input">
          <input id="confirm-reconnect-delay" type="number" min={MIN_CONFIRM_RECONNECT_DELAY_MS} max={MAX_CONFIRM_RECONNECT_DELAY_MS} step="50" bind:value={confirmReconnectDelayDraft.draft} onchange={commitConfirmReconnectDelay} disabled={confirmReconnectDelayBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Stabilization wait before a confirmation reconnect. Allowed range: 50–2000 milliseconds.')}</p>
    {:else if confirmReconnectDelayError}
      <div class="alert danger">{confirmReconnectDelayError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onConfirmReconnectDelayRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Shuffle size={12} /> {t('Channel and presence')}</h4>
    {#if channelConfirmAttempts !== null}
      <div class="delay-row">
        <label for="channel-confirm-attempts">{t('Channel confirmation attempts')}</label>
        <span class="delay-input">
          <input id="channel-confirm-attempts" type="number" min={MIN_CHANNEL_CONFIRM_ATTEMPTS} max={MAX_CHANNEL_CONFIRM_ATTEMPTS} step="1" bind:value={channelConfirmAttemptsDraft.draft} onchange={commitChannelConfirmAttempts} disabled={channelConfirmAttemptsBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many readbacks are tried after a channel change before it is reported unconfirmed. Allowed range: 1–20.')}</p>
    {:else if channelConfirmAttemptsError}
      <div class="alert danger">{channelConfirmAttemptsError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onChannelConfirmAttemptsRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading channel and presence settings...')}</p>
    {/if}

    {#if channelConfirmIntervalMs !== null}
      <div class="delay-row">
        <label for="channel-confirm-interval">{t('Channel confirmation interval')}</label>
        <span class="delay-input">
          <input id="channel-confirm-interval" type="number" min={MIN_CHANNEL_CONFIRM_INTERVAL_MS} max={MAX_CHANNEL_CONFIRM_INTERVAL_MS} step="50" bind:value={channelConfirmIntervalDraft.draft} onchange={commitChannelConfirmInterval} disabled={channelConfirmIntervalBusy} />
          {t('milliseconds')}
        </span>
      </div>
      <p class="hint">{t('Pause between channel confirmation readbacks. Allowed range: 50–2000 milliseconds.')}</p>
    {:else if channelConfirmIntervalError}
      <div class="alert danger">{channelConfirmIntervalError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onChannelConfirmIntervalRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if presenceMissThreshold !== null}
      <div class="delay-row">
        <label for="presence-miss-threshold">{t('Absence miss threshold')}</label>
        <span class="delay-input">
          <input id="presence-miss-threshold" type="number" min={MIN_PRESENCE_MISS_THRESHOLD} max={MAX_PRESENCE_MISS_THRESHOLD} step="1" bind:value={presenceMissThresholdDraft.draft} onchange={commitPresenceMissThreshold} disabled={presenceMissThresholdBusy} />
          {t('scans')}
        </span>
      </div>
      <p class="hint">{t('How many consecutive scans must miss a station before it is marked absent. Allowed range: 1–10.')}</p>
    {:else if presenceMissThresholdError}
      <div class="alert danger">{presenceMissThresholdError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onPresenceMissThresholdRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Network size={12} /> {t('HTTP API')}</h4>
    {#if apiListenAddress !== null}
      <div class="delay-row">
        <label for="api-listen-address">{t('Listen address')}</label>
        <span class="delay-input wide">
          <input id="api-listen-address" type="text" bind:value={apiAddressDraft} onchange={commitAPIListenAddress} disabled={apiListenAddressBusy} />
        </span>
      </div>
      <p class="hint">{t('Host and port for the local HTTP API used by external tools. The server restarts on the new address after saving. Example: 127.0.0.1:7575. Ports must be between 1024 and 65535.')}</p>
    {:else if apiListenAddressError}
      <div class="alert danger">{apiListenAddressError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onAPIListenAddressRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading HTTP API settings...')}</p>
    {/if}
  </section>

  <section>
    <h4><Hourglass size={12} /> {t('Read budgets')}</h4>
    {#if initialReadTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="initial-read-timeout">{t('Initial read timeout')}</label>
        <span class="delay-input">
          <input id="initial-read-timeout" type="number" min={MIN_INITIAL_READ_TIMEOUT_SECONDS} max={MAX_INITIAL_READ_TIMEOUT_SECONDS} step="1" bind:value={initialReadTimeoutDraft.draft} onchange={commitInitialReadTimeout} disabled={initialReadTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Per-station budget for connection plus the first state read. Cannot exceed the station operation timeout. Allowed range: 10–60 seconds.')}</p>
    {:else if initialReadTimeoutError}
      <div class="alert danger">{initialReadTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onInitialReadTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading read budget settings...')}</p>
    {/if}

    {#if scanReadPhaseTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="scan-read-phase-timeout">{t('Scan read phase timeout')}</label>
        <span class="delay-input">
          <input id="scan-read-phase-timeout" type="number" min={MIN_SCAN_READ_PHASE_TIMEOUT_SECONDS} max={MAX_SCAN_READ_PHASE_TIMEOUT_SECONDS} step="1" bind:value={scanReadPhaseTimeoutDraft.draft} onchange={commitScanReadPhaseTimeout} disabled={scanReadPhaseTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Total budget for the initial-read phase after a scan; must cover the initial read timeout. Allowed range: 15–120 seconds.')}</p>
    {:else if scanReadPhaseTimeoutError}
      <div class="alert danger">{scanReadPhaseTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onScanReadPhaseTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if statusReadTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="status-read-timeout">{t('Status read timeout')}</label>
        <span class="delay-input">
          <input id="status-read-timeout" type="number" min={MIN_STATUS_READ_TIMEOUT_SECONDS} max={MAX_STATUS_READ_TIMEOUT_SECONDS} step="1" bind:value={statusReadTimeoutDraft.draft} onchange={commitStatusReadTimeout} disabled={statusReadTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Per-station timeout during periodic status refreshes. Allowed range: 5–60 seconds.')}</p>
    {:else if statusReadTimeoutError}
      <div class="alert danger">{statusReadTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onStatusReadTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if statusRefreshTimeoutSeconds !== null}
      <div class="delay-row">
        <label for="status-refresh-timeout">{t('Status refresh timeout')}</label>
        <span class="delay-input">
          <input id="status-refresh-timeout" type="number" min={MIN_STATUS_REFRESH_TIMEOUT_SECONDS} max={MAX_STATUS_REFRESH_TIMEOUT_SECONDS} step="1" bind:value={statusRefreshTimeoutDraft.draft} onchange={commitStatusRefreshTimeout} disabled={statusRefreshTimeoutBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Total budget for one periodic fleet status refresh; must cover the status read timeout. Allowed range: 10–120 seconds.')}</p>
    {:else if statusRefreshTimeoutError}
      <div class="alert danger">{statusRefreshTimeoutError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onStatusRefreshTimeoutRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if channelScanFreshnessSeconds !== null}
      <div class="delay-row">
        <label for="channel-scan-freshness">{t('Channel scan freshness')}</label>
        <span class="delay-input">
          <input id="channel-scan-freshness" type="number" min={MIN_CHANNEL_SCAN_FRESHNESS_SECONDS} max={MAX_CHANNEL_SCAN_FRESHNESS_SECONDS} step="10" bind:value={channelScanFreshnessDraft.draft} onchange={commitChannelScanFreshness} disabled={channelScanFreshnessBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('How long ago a station may have last been seen in a scan before its channel is treated as unknown. Allowed range: 30–600 seconds.')}</p>
    {:else if channelScanFreshnessError}
      <div class="alert danger">{channelScanFreshnessError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onChannelScanFreshnessRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><History size={12} /> {t('Background recovery')}</h4>
    {#if recoveryRetryBaseSeconds !== null}
      <div class="delay-row">
        <label for="recovery-retry-base">{t('Recovery retry base')}</label>
        <span class="delay-input">
          <input id="recovery-retry-base" type="number" min={MIN_RECOVERY_RETRY_BASE_SECONDS} max={MAX_RECOVERY_RETRY_BASE_SECONDS} step="5" bind:value={recoveryRetryBaseDraft.draft} onchange={commitRecoveryRetryBase} disabled={recoveryRetryBaseBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Base delay for reconnecting a lost station; doubles per failure up to the maximum. Must not exceed the recovery retry maximum. Allowed range: 5–120 seconds.')}</p>
    {:else if recoveryRetryBaseError}
      <div class="alert danger">{recoveryRetryBaseError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onRecoveryRetryBaseRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Loading recovery settings...')}</p>
    {/if}

    {#if recoveryRetryMaxSeconds !== null}
      <div class="delay-row">
        <label for="recovery-retry-max">{t('Recovery retry maximum')}</label>
        <span class="delay-input">
          <input id="recovery-retry-max" type="number" min={MIN_RECOVERY_RETRY_MAX_SECONDS} max={MAX_RECOVERY_RETRY_MAX_SECONDS} step="30" bind:value={recoveryRetryMaxDraft.draft} onchange={commitRecoveryRetryMax} disabled={recoveryRetryMaxBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Upper bound of the escalating station recovery delay. Must not fall below the recovery retry base. Allowed range: 60–1800 seconds.')}</p>
    {:else if recoveryRetryMaxError}
      <div class="alert danger">{recoveryRetryMaxError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onRecoveryRetryMaxRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if absentStationRetryLimit !== null}
      <div class="delay-row">
        <label for="absent-station-retry-limit">{t('Absent station retry limit')}</label>
        <span class="delay-input">
          <input id="absent-station-retry-limit" type="number" min={MIN_ABSENT_STATION_RETRY_LIMIT} max={MAX_ABSENT_STATION_RETRY_LIMIT} step="1" bind:value={absentStationRetryLimitDraft.draft} onchange={commitAbsentStationRetryLimit} disabled={absentStationRetryLimitBusy} />
          {t('attempts')}
        </span>
      </div>
      <p class="hint">{t('How many recovery attempts an absent station gets before its recovery schedule stops. Allowed range: 1–20.')}</p>
    {:else if absentStationRetryLimitError}
      <div class="alert danger">{absentStationRetryLimitError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onAbsentStationRetryLimitRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}

    {#if bluetoothInitRetrySeconds !== null}
      <div class="delay-row">
        <label for="bluetooth-init-retry">{t('Adapter init retry')}</label>
        <span class="delay-input">
          <input id="bluetooth-init-retry" type="number" min={MIN_BLUETOOTH_INIT_RETRY_SECONDS} max={MAX_BLUETOOTH_INIT_RETRY_SECONDS} step="1" bind:value={bluetoothInitRetryDraft.draft} onchange={commitBluetoothInitRetry} disabled={bluetoothInitRetryBusy} />
          {t('seconds')}
        </span>
      </div>
      <p class="hint">{t('Cooldown before the Bluetooth adapter initialization is retried after a failure. Allowed range: 1–30 seconds.')}</p>
    {:else if bluetoothInitRetryError}
      <div class="alert danger">{bluetoothInitRetryError}</div>
      <div class="drawer-actions"><button class="btn" onclick={() => onBluetoothInitRetryRetry?.()}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {/if}
  </section>

  <section>
    <h4><Bluetooth size={12} /> {t('Bluetooth diagnostics')}</h4>
    {#if loading}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Detecting Bluetooth adapters...')}</p>
    {:else if loadError}
      <div class="alert danger">{loadError}</div>
      <div class="drawer-actions"><button class="btn" onclick={onRefresh}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <div class="adapter-list" aria-label={t('Detected Bluetooth adapters')}>
        {#each adapters as adapter (adapter.deviceId)}
          <div class="adapter-option"><span class="adapter-text"><span class="adapter-name">{adapter.name}</span><span class="adapter-id">{adapter.deviceId}</span></span></div>
        {:else}
          <p class="hint">{t('No Bluetooth adapters were detected on this system.')}</p>
        {/each}
      </div>
      <div class="drawer-actions">
        <button class="btn" onclick={onRefresh} disabled={loading}>{#if loading}<LoaderCircle class="spin" size={15} />{:else}<RefreshCw size={15} />{/if} {t('Refresh adapters')}</button>
      </div>
      <p class="hint">{t('Windows controls which radio handles BLE discovery and connections. The application cannot route a Lighthouse operation through one specific adapter.')}</p>
    {/if}
  </section>
</div>

<style>
  .drawer {
    position: fixed;
    z-index: 11;
    right: 0; top: 0; bottom: 0;
    width: min(390px, 92vw);
    background:
      linear-gradient(180deg,
        color-mix(in srgb, var(--color-primary) 65%, transparent),
        color-mix(in srgb, var(--color-on) 65%, transparent) 33%,
        color-mix(in srgb, var(--color-standby) 65%, transparent) 66%,
        color-mix(in srgb, var(--color-sleep) 65%, transparent))
        0 12px / 2px calc(100% - 24px) no-repeat,
      var(--bg-surface-solid);
    border-left: 1px solid var(--color-border);
    border-radius: var(--radius-lg) 0 0 var(--radius-lg);
    padding: 1rem 1rem 1.25rem;
    overflow: auto;
    box-shadow: var(--shadow-lg);
  }
  section {
    border-top: 1px solid var(--color-border);
    padding-top: 0.85rem;
    margin-top: 0.85rem;
    animation: rise var(--dur-3) var(--ease) backwards;
    animation-delay: 60ms;
  }
  h4 {
    margin: 0 0 0.55rem;
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--fs-micro);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text-muted);
  }
  h4::before {
    content: '';
    width: 3px;
    height: 11px;
    border-radius: 2px;
    background: linear-gradient(180deg, var(--color-primary), var(--color-sleep));
  }
  .adapter-list { display: flex; flex-direction: column; gap: 0.4rem; }
  .adapter-option {
    display: flex;
    align-items: flex-start;
    gap: 0.55rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--bg-input);
    cursor: default;
    transition:
      border-color var(--dur-1) var(--ease),
      background-color var(--dur-1) var(--ease),
      box-shadow var(--dur-1) var(--ease);
  }
  .adapter-option.selected {
    border-color: var(--color-primary);
    background: color-mix(in srgb, var(--color-primary) 9%, var(--bg-input));
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-primary) 10%, transparent);
  }
  .adapter-text { display: flex; flex-direction: column; gap: 0.1rem; min-width: 0; }
  .adapter-name {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: var(--fs-sm);
    font-weight: 700;
    color: var(--text-primary);
  }
  .adapter-desc { font-size: var(--fs-micro); font-weight: 600; color: var(--text-muted); }
  .adapter-id {
    font-family: var(--font-mono);
    font-size: 0.65rem;
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }
  .drawer-actions { display: flex; flex-wrap: wrap; gap: 0.3rem; margin-top: 0.8rem; }
  .drawer-actions .btn {
    flex: 0 0 auto;
    min-height: 30px;
    gap: 0.25rem;
    padding: 0.35rem 0.55rem;
    font-size: var(--fs-micro);
    white-space: nowrap;
  }
  .switch-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--bg-input);
    cursor: pointer;
  }
  .switch-row:has(input:disabled) { cursor: wait; }
  .hint + .switch-row, .drawer-actions + .switch-row { margin-top: 0.55rem; }
  .switch {
    appearance: none;
    position: relative;
    flex-shrink: 0;
    width: 36px;
    height: 20px;
    border-radius: var(--radius-pill);
    border: 1px solid var(--color-border-strong);
    background: var(--bg-inset);
    cursor: pointer;
    transition: background-color var(--dur-1) var(--ease), border-color var(--dur-1) var(--ease);
  }
  .switch:checked {
    background: var(--color-primary);
    border-color: var(--color-primary);
  }
  .switch::after {
    content: '';
    position: absolute;
    top: 1px; left: 1px;
    width: 16px; height: 16px;
    border-radius: 50%;
    background: #fff;
    box-shadow: var(--shadow-sm);
    transition: transform var(--dur-1) var(--ease);
  }
  .switch:checked::after { transform: translateX(16px); }
  .switch:disabled { opacity: 0.5; cursor: not-allowed; }
  .delay-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.6rem;
    margin-top: 0.55rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--bg-input);
    font-size: var(--fs-sm);
    font-weight: 700;
    color: var(--text-primary);
  }
  .delay-input {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--fs-micro);
    font-weight: 600;
    color: var(--text-muted);
  }
  .delay-input input { width: 4.2rem; text-align: right; }
  .delay-input.wide input { width: 11rem; text-align: left; }
  .hint {
    margin: 0.6rem 0 0;
    font-size: var(--fs-micro);
    font-weight: 600;
    color: var(--text-muted);
    line-height: 1.5;
  }
  .hint.loading { display: flex; align-items: center; gap: 0.4rem; }
</style>
