// Browser-only development fallback. `vite dev` outside `wails dev` has no
// window.go runtime; without a shim every binding rejects with a TypeError
// and the UI renders nothing but permanent failures. The shim answers with a
// harmless empty fleet so layouts and flows stay inspectable.
type GoNamespace = Record<string, (...args: unknown[]) => Promise<unknown>>;

function isRuntimeMissing(): boolean {
  return !('go' in window);
}

function installEventRuntime() {
  if ('runtime' in window) return;
  Object.defineProperty(window, 'runtime', {
    value: {
      EventsOnMultiple: () => () => {},
      EventsOff: () => {},
      EventsOffAll: () => {},
      LogPrint: () => {},
      LogTrace: () => {},
      LogDebug: () => {},
      LogInfo: () => {},
      LogWarning: () => {},
      LogError: () => {},
      LogFatal: () => {}
    },
    configurable: true
  });
}

if (import.meta.env.DEV && isRuntimeMissing()) {
  const unavailable = (name: string) => () =>
    Promise.reject(new Error(`${name} is unavailable in the browser development preview`));
  const app: GoNamespace = {
    CheckAllStationStatuses: () => Promise.resolve([]),
    CancelBulkPower: () => Promise.resolve(undefined),
    GetAbsentStationRetryLimit: () => Promise.resolve(5),
    GetAPIStatus: () => Promise.resolve({
      running: false,
      address: '',
      error: 'browser development preview',
      warnings: [] as string[],
      configWritable: true,
      activeOperations: [],
      operationRevision: 0
    }),
    GetAPIListenAddress: () => Promise.resolve('127.0.0.1:7575'),
    GetAutoSleepSettings: () => Promise.resolve({ enabled: false, target: 'steamvr', delaySeconds: 300 }),
    GetBluetoothInitRetrySeconds: () => Promise.resolve(2),
    GetBootFallbackSeconds: () => Promise.resolve(8),
    GetBulkPowerTimeoutSeconds: () => Promise.resolve(120),
    GetChannelConfirmAttempts: () => Promise.resolve(5),
    GetChannelConfirmIntervalMs: () => Promise.resolve(250),
    GetChannelScanFreshnessSeconds: () => Promise.resolve(120),
    GetConfirmReconnectDelayMs: () => Promise.resolve(250),
    GetConfirmReconnectThreshold: () => Promise.resolve(2),
    GetDiscoveryAttempts: () => Promise.resolve(3),
    GetDiscoveryRetryDelayMs: () => Promise.resolve(500),
    GetIdentifyAttempts: () => Promise.resolve(2),
    GetInitialReadTimeoutSeconds: () => Promise.resolve(30),
    GetLanguage: () => Promise.resolve(''),
    GetOperationRetryDelayMs: () => Promise.resolve(500),
    GetPowerConfirmAttemptsOff: () => Promise.resolve(15),
    GetPowerConfirmAttemptsOn: () => Promise.resolve(51),
    GetPowerConfirmPollIntervalMs: () => Promise.resolve(200),
    GetPowerWriteAttempts: () => Promise.resolve(2),
    GetPresenceMissThreshold: () => Promise.resolve(2),
    GetRecoveryRetryBaseSeconds: () => Promise.resolve(30),
    GetRecoveryRetryMaxSeconds: () => Promise.resolve(300),
    GetScanDurationSeconds: () => Promise.resolve(5),
    GetScanOnStartup: () => Promise.resolve(true),
    GetScanReadPhaseTimeoutSeconds: () => Promise.resolve(45),
    GetSleepFinalWriteTimeoutSeconds: () => Promise.resolve(30),
    GetSleepPrepareGapMs: () => Promise.resolve(50),
    GetStationOperationTimeoutSeconds: () => Promise.resolve(30),
    GetStatusReadTimeoutSeconds: () => Promise.resolve(20),
    GetStatusRefreshTimeoutSeconds: () => Promise.resolve(30),
    GetStatusPollIntervalSeconds: () => Promise.resolve(15),
    GetStatusPollingEnabled: () => Promise.resolve(true),
    GetCurrentStationInfo: () => Promise.resolve([]),
    // Shape matches station.ScanStatus (id omitted, like the backend's
    // omitempty) so development previews exercise the same field presence.
    GetScanStatus: () => Promise.resolve({ state: 'completed', startedAt: '', completedAt: '', found: 0, error: '', warnings: [] }),
    IdentifyStation: unavailable('IdentifyStation'),
    IsScanning: () => Promise.resolve(false),
    ListBluetoothAdapters: () => Promise.resolve([]),
    RefreshStationCapabilities: unavailable('RefreshStationCapabilities'),
    RenameStationByAddress: unavailable('RenameStationByAddress'),
    ScanAndFetchStations: () => new Promise((resolve) => setTimeout(() => resolve([]), 400)),
    SetAllStationsPowerDetailed: () => Promise.reject(new Error('SetAllStationsPowerDetailed is unavailable in the browser development preview')),
    SetAbsentStationRetryLimit: unavailable('SetAbsentStationRetryLimit'),
    SetAPIListenAddress: unavailable('SetAPIListenAddress'),
    SetAutoSleepSettings: unavailable('SetAutoSleepSettings'),
    SetBluetoothInitRetrySeconds: unavailable('SetBluetoothInitRetrySeconds'),
    SetBootFallbackSeconds: unavailable('SetBootFallbackSeconds'),
    SetBulkPowerTimeoutSeconds: unavailable('SetBulkPowerTimeoutSeconds'),
    SetChannelConfirmAttempts: unavailable('SetChannelConfirmAttempts'),
    SetChannelConfirmIntervalMs: unavailable('SetChannelConfirmIntervalMs'),
    SetChannelScanFreshnessSeconds: unavailable('SetChannelScanFreshnessSeconds'),
    SetConfirmReconnectDelayMs: unavailable('SetConfirmReconnectDelayMs'),
    SetConfirmReconnectThreshold: unavailable('SetConfirmReconnectThreshold'),
    SetDiscoveryAttempts: unavailable('SetDiscoveryAttempts'),
    SetDiscoveryRetryDelayMs: unavailable('SetDiscoveryRetryDelayMs'),
    SetIdentifyAttempts: unavailable('SetIdentifyAttempts'),
    SetInitialReadTimeoutSeconds: unavailable('SetInitialReadTimeoutSeconds'),
    SetLanguage: unavailable('SetLanguage'),
    SetOperationRetryDelayMs: unavailable('SetOperationRetryDelayMs'),
    SetPowerConfirmAttemptsOff: unavailable('SetPowerConfirmAttemptsOff'),
    SetPowerConfirmAttemptsOn: unavailable('SetPowerConfirmAttemptsOn'),
    SetPowerConfirmPollIntervalMs: unavailable('SetPowerConfirmPollIntervalMs'),
    SetPowerWriteAttempts: unavailable('SetPowerWriteAttempts'),
    SetPresenceMissThreshold: unavailable('SetPresenceMissThreshold'),
    SetRecoveryRetryBaseSeconds: unavailable('SetRecoveryRetryBaseSeconds'),
    SetRecoveryRetryMaxSeconds: unavailable('SetRecoveryRetryMaxSeconds'),
    SetScanDurationSeconds: unavailable('SetScanDurationSeconds'),
    SetScanReadPhaseTimeoutSeconds: unavailable('SetScanReadPhaseTimeoutSeconds'),
    SetSleepFinalWriteTimeoutSeconds: unavailable('SetSleepFinalWriteTimeoutSeconds'),
    SetSleepPrepareGapMs: unavailable('SetSleepPrepareGapMs'),
    SetStationOperationTimeoutSeconds: unavailable('SetStationOperationTimeoutSeconds'),
    SetStatusReadTimeoutSeconds: unavailable('SetStatusReadTimeoutSeconds'),
    SetStatusRefreshTimeoutSeconds: unavailable('SetStatusRefreshTimeoutSeconds'),
    SetScanOnStartup: unavailable('SetScanOnStartup'),
    SetStatusPollIntervalSeconds: unavailable('SetStatusPollIntervalSeconds'),
    SetStatusPollingEnabled: unavailable('SetStatusPollingEnabled'),
    SetStationChannel: unavailable('SetStationChannel'),
    SetStationPower: unavailable('SetStationPower'),
    StopScan: () => Promise.resolve(undefined)
  };
  Object.defineProperty(window, 'go', {
    value: { main: { App: app } },
    configurable: true
  });
}

if (import.meta.env.DEV) {
  installEventRuntime();
}

export {};
