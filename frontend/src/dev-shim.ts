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
    GetAPIStatus: () => Promise.resolve({
      running: false,
      address: '',
      error: 'browser development preview',
      warnings: [] as string[],
      configWritable: true
    }),
    GetAutoSleepSettings: () => Promise.resolve({ enabled: false, target: 'steamvr', delaySeconds: 300 }),
	GetBulkPowerTimeoutSeconds: () => Promise.resolve(120),
	GetLanguage: () => Promise.resolve(''),
	GetStatusPollIntervalSeconds: () => Promise.resolve(15),
    GetCurrentStationInfo: () => Promise.resolve([]),
    GetScanStatus: () => Promise.resolve({ state: 'completed', found: 0, error: '', warnings: [] }),
    IdentifyStation: unavailable('IdentifyStation'),
    IsScanning: () => Promise.resolve(false),
    ListBluetoothAdapters: () => Promise.resolve([]),
    RefreshStationCapabilities: unavailable('RefreshStationCapabilities'),
    RenameStationByAddress: unavailable('RenameStationByAddress'),
    ScanAndFetchStations: () => new Promise((resolve) => setTimeout(() => resolve([]), 400)),
    SetAllStationsPowerDetailed: () => Promise.reject(new Error('SetAllStationsPowerDetailed is unavailable in the browser development preview')),
    SetAutoSleepSettings: unavailable('SetAutoSleepSettings'),
	SetBulkPowerTimeoutSeconds: unavailable('SetBulkPowerTimeoutSeconds'),
	SetLanguage: unavailable('SetLanguage'),
	SetStatusPollIntervalSeconds: unavailable('SetStatusPollIntervalSeconds'),
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
