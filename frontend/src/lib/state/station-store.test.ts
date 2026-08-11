import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  CheckAllStationStatuses: vi.fn(),
  CancelBulkPower: vi.fn(),
  GetAPIStatus: vi.fn(),
  GetCurrentStationInfo: vi.fn(),
  GetScanOnStartup: vi.fn(),
  GetScanStatus: vi.fn(),
  GetStatusPollIntervalSeconds: vi.fn(),
  GetStatusPollingEnabled: vi.fn(),
  IdentifyStation: vi.fn(),
  IsScanning: vi.fn(),
  RefreshStationCapabilities: vi.fn(),
  RenameStationByAddress: vi.fn(),
  ScanAndFetchStations: vi.fn(),
  SetAllStationsPowerDetailed: vi.fn(),
  SetStationChannel: vi.fn(),
  SetStationPower: vi.fn(),
  StopScan: vi.fn()
}));

const runtime = vi.hoisted(() => ({
  handlers: new Map<string, (...args: unknown[]) => void>(),
  EventsOn: vi.fn((name: string, callback: (...args: unknown[]) => void) => {
    runtime.handlers.set(name, callback);
    return () => runtime.handlers.delete(name);
  })
}));

vi.mock('../backend', () => backend);
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: runtime.EventsOn }));
vi.mock('../toast', async (importOriginal) => {
  const original = await importOriginal<typeof import('../toast')>();
  return { ...original, pushToast: vi.fn() };
});

import { StationStore, type StationStoreUi } from './station-store.svelte.ts';
import { pushToast } from '../toast';
import { createStation } from '../../test/fixtures';
import { setLanguagePreference } from '../i18n.svelte';

function createUi(): StationStoreUi {
  return {
    closeChannelEditor: vi.fn(),
    forceCloseChannelEditor: vi.fn(),
    clearBulkConfirmation: vi.fn(),
    requestBulkConfirmation: vi.fn()
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

let store: StationStore | null = null;

function mountStore(ui = createUi()) {
  store = new StationStore(ui);
  store.mount();
  return { store, ui };
}

beforeEach(() => {
  setLanguagePreference('en');
  vi.clearAllMocks();
  runtime.handlers.clear();
  backend.IsScanning.mockResolvedValue(false);
  backend.GetScanOnStartup.mockResolvedValue(true);
  backend.GetAPIStatus.mockResolvedValue({
    running: true, address: '127.0.0.1:7575', error: '', warnings: [], configWritable: true
  });
  backend.ScanAndFetchStations.mockResolvedValue([createStation()]);
  backend.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
  backend.GetCurrentStationInfo.mockResolvedValue([createStation()]);
  backend.GetStatusPollIntervalSeconds.mockResolvedValue(15);
  backend.GetStatusPollingEnabled.mockResolvedValue(true);
  backend.CheckAllStationStatuses.mockResolvedValue([createStation()]);
  backend.StopScan.mockResolvedValue(undefined);
  backend.SetStationPower.mockResolvedValue({
    station: createStation(), commandSent: true, confirmed: true, confirmationError: ''
  });
});

describe('StationStore projection settings', () => {
  it('refreshes derived station fields without performing a Bluetooth status read', async () => {
    store = new StationStore(createUi());
    store.startupPending = false;
    store.stations = [createStation({ isPresent: true, powerFresh: true })];
    backend.GetCurrentStationInfo.mockResolvedValue([
      createStation({ isPresent: false, powerFresh: false, statusFresh: false })
    ]);

    await store.refreshStationProjection();

    expect(backend.GetCurrentStationInfo).toHaveBeenCalledOnce();
    expect(backend.CheckAllStationStatuses).not.toHaveBeenCalled();
    expect(store.stations[0].isPresent).toBe(false);
    expect(store.stations[0].powerFresh).toBe(false);
  });

  it('defers a projection refresh until an authoritative operation settles', async () => {
    vi.useFakeTimers();
    store = new StationStore(createUi());
    store.startupPending = false;
    store.globalOperation = 'scanning';
    backend.GetCurrentStationInfo.mockResolvedValue([
      createStation({ isPresent: false, scanFresh: false })
    ]);

    await store.refreshStationProjection();

    expect(backend.GetCurrentStationInfo).not.toHaveBeenCalled();
    store.globalOperation = 'idle';
    await vi.advanceTimersByTimeAsync(249);
    expect(backend.GetCurrentStationInfo).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);

    expect(backend.GetCurrentStationInfo).toHaveBeenCalledOnce();
    expect(store.stations[0].isPresent).toBe(false);
    expect(store.stations[0].scanFresh).toBe(false);
  });

  it('retries a transient projection read failure while station polling is disabled', async () => {
    vi.useFakeTimers();
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    store = new StationStore(createUi());
    store.startupPending = false;
    store.stations = [createStation({ isPresent: true, powerFresh: true })];
    backend.GetCurrentStationInfo
      .mockRejectedValueOnce(new Error('temporary projection failure'))
      .mockResolvedValueOnce([createStation({ isPresent: false, powerFresh: false, statusFresh: false })]);

    await store.refreshStationProjection();

    expect(backend.GetCurrentStationInfo).toHaveBeenCalledOnce();
    expect(store.stations[0].isPresent).toBe(true);
    await vi.advanceTimersByTimeAsync(249);
    expect(backend.GetCurrentStationInfo).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(1);

    expect(backend.GetCurrentStationInfo).toHaveBeenCalledTimes(2);
    expect(store.stations[0].isPresent).toBe(false);
    expect(store.stations[0].powerFresh).toBe(false);
    consoleError.mockRestore();
  });

  it('refreshes station status immediately when automatic polling is re-enabled', async () => {
    store = new StationStore(createUi());
    store.startupPending = false;
    store.setStatusPollingEnabled(false);
    backend.CheckAllStationStatuses.mockClear();

    store.setStatusPollingEnabled(true);

    await vi.waitFor(() => expect(backend.CheckAllStationStatuses).toHaveBeenCalledOnce());
  });
});

describe('StationStore locale changes', () => {
  it('rebuilds transient messages and clears old-language feedback', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    store.statusMessage = 'Old status';
    store.channelError = 'Old channel error';
    store.powerFeedback.set(store.stations[0].address, {
      kind: 'success', text: 'Old feedback', target: 'on'
    });

    setLanguagePreference('zh-CN');
    store.onLocaleChanged();

    expect(store.statusMessage).toBe('可以开始扫描。');
    expect(store.channelError).toBe('');
    expect(store.powerFeedbackMap).toEqual({});
  });

  it('rebuilds the status line for a running external operation', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    store.externalOperationRunning = true;

    setLanguagePreference('zh-CN');
    store.onLocaleChanged();

    expect(store.statusMessage).toBe('蓝牙操作正在进行');
  });

  it('keeps the specific auto-sleep status while its tracked operation is active', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    store.externalOperationRunning = true;
    store.autoSleepRunning = true;

    setLanguagePreference('en');
    store.onLocaleChanged();

    expect(store.statusMessage).toBe('Auto sleep: scanning and putting all stations to sleep...');
  });
});

afterEach(() => {
  store?.dispose();
  store = null;
  vi.useRealTimers();
});

describe('StationStore startup', () => {
  it('does not let a delayed startup polling preference override a saved change', async () => {
    vi.useFakeTimers();
    const startupPreference = deferred<boolean>();
    backend.GetScanOnStartup.mockResolvedValue(false);
    backend.GetStatusPollingEnabled.mockReturnValue(startupPreference.promise);
    const { store } = mountStore();

    // The persisted value was false when the startup read began. The user
    // enables it before that old response arrives; true equals the runtime
    // default, so this also covers the otherwise easy-to-miss no-op path.
    store.setStatusPollingEnabled(true);
    startupPreference.resolve(false);
    await vi.advanceTimersByTimeAsync(0);
    backend.CheckAllStationStatuses.mockClear();

    await vi.advanceTimersByTimeAsync(15_000);

    expect(backend.CheckAllStationStatuses).toHaveBeenCalledOnce();
  });

  it('does not let a delayed startup interval override a saved change', async () => {
    vi.useFakeTimers();
    const startupInterval = deferred<number>();
    backend.GetScanOnStartup.mockResolvedValue(false);
    backend.GetStatusPollIntervalSeconds.mockReturnValue(startupInterval.promise);
    const { store } = mountStore();

    // The user restores the 15-second default while a stale persisted
    // 30-second response is in flight. The explicit save must still win even
    // though applying 15 seconds is an immediate runtime no-op.
    store.setStatusPollIntervalSeconds(15);
    startupInterval.resolve(30);
    await vi.advanceTimersByTimeAsync(0);
    backend.CheckAllStationStatuses.mockClear();

    await vi.advanceTimersByTimeAsync(15_000);

    expect(backend.CheckAllStationStatuses).toHaveBeenCalledOnce();
  });

  it('ignores delayed startup preferences after disposal', async () => {
    vi.useFakeTimers();
    const startupInterval = deferred<number>();
    backend.GetScanOnStartup.mockResolvedValue(false);
    backend.GetStatusPollIntervalSeconds.mockReturnValue(startupInterval.promise);
    const { store } = mountStore();
    await vi.advanceTimersByTimeAsync(0);
    const healthCallsBeforeDispose = backend.GetAPIStatus.mock.calls.length;

    store.dispose();
    startupInterval.resolve(5);
    await vi.advanceTimersByTimeAsync(15_000);

    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(healthCallsBeforeDispose);
    expect(backend.CheckAllStationStatuses).not.toHaveBeenCalled();
  });

  it('uses the persisted automatic polling interval', async () => {
    vi.useFakeTimers();
    backend.GetStatusPollIntervalSeconds.mockResolvedValue(30);
    mountStore();
    await vi.advanceTimersByTimeAsync(0);
    expect(backend.GetStatusPollIntervalSeconds).toHaveBeenCalledOnce();
    backend.CheckAllStationStatuses.mockClear();

    await vi.advanceTimersByTimeAsync(29_999);
    expect(backend.CheckAllStationStatuses).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(backend.CheckAllStationStatuses).toHaveBeenCalledOnce();
  });

  it('runs the initial scan on mount when the backend is idle', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(backend.ScanAndFetchStations).toHaveBeenCalledOnce());
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    expect(store.stations[0].name).toBe('LHB-TEST');
    expect(store.statusMessage).toBe('Found 1; 1 known station.');
    expect(store.scanError).toBeNull();
  });

  it('respects a disabled startup scan preference', async () => {
    backend.GetScanOnStartup.mockResolvedValue(false);
    mountStore();
    await vi.waitFor(() => expect(backend.GetScanOnStartup).toHaveBeenCalledOnce());
    await Promise.resolve();
    expect(backend.ScanAndFetchStations).not.toHaveBeenCalled();
  });

  it('keeps cached freshness current without automatic Bluetooth station reads', async () => {
    vi.useFakeTimers();
    backend.GetStatusPollingEnabled.mockResolvedValue(false);
    backend.GetScanOnStartup.mockResolvedValue(false);
    mountStore();
    await vi.advanceTimersByTimeAsync(0);
    backend.CheckAllStationStatuses.mockClear();
    backend.GetAPIStatus.mockClear();
    backend.GetCurrentStationInfo.mockClear();

    await vi.advanceTimersByTimeAsync(30_000);

    expect(backend.CheckAllStationStatuses).not.toHaveBeenCalled();
    expect(backend.GetAPIStatus).toHaveBeenCalled();
    expect(backend.GetCurrentStationInfo).toHaveBeenCalled();
  });

  it('adopts a running backend scan instead of starting a local scan', async () => {
    backend.IsScanning.mockResolvedValue(true);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.externalScanning).toBe(true));
    expect(backend.ScanAndFetchStations).not.toHaveBeenCalled();
  });

  it('does not adopt a backend scan that ends during startup classification', async () => {
    backend.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(false);

    const { store } = mountStore();
    await vi.waitFor(() => expect(backend.IsScanning).toHaveBeenCalledTimes(2));

    expect(store.externalScanning).toBe(false);
    expect(backend.ScanAndFetchStations).not.toHaveBeenCalled();
  });

  it('does not adopt an automatic-sleep scan whose start event was missed', async () => {
    backend.IsScanning.mockResolvedValue(true);
    backend.GetAPIStatus.mockResolvedValue({
      running: true,
      address: '127.0.0.1:7575',
      error: '',
      warnings: [],
      configWritable: true,
      activeOperations: [{ id: 41, kind: 'auto-sleep' }],
      operationRevision: 1
    });

    const { store } = mountStore();
    await vi.waitFor(() => expect(store.externalOperationRunning).toBe(true));
    await vi.waitFor(() => expect(backend.IsScanning).toHaveBeenCalled());

    expect(store.externalScanning).toBe(false);
    expect(backend.ScanAndFetchStations).not.toHaveBeenCalled();
  });

  it('waits for the restarted health poll before classifying a startup scan', async () => {
    backend.IsScanning.mockResolvedValue(true);
    backend.GetStatusPollIntervalSeconds.mockResolvedValue(30);
    let resolveFirstStatus!: (value: Record<string, unknown>) => void;
    let resolveFinalStatus!: (value: Record<string, unknown>) => void;
    backend.GetAPIStatus
      .mockReturnValueOnce(new Promise((resolve) => { resolveFirstStatus = resolve; }))
      .mockReturnValueOnce(new Promise((resolve) => { resolveFinalStatus = resolve; }));

    const { store } = mountStore();
    await vi.waitFor(() => expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2));

    // Changing the persisted interval restarts the health poll. Its older
    // response is revision-gated, so startup must not use that completion as
    // proof that no internal operation is active.
    resolveFirstStatus({
      running: true,
      address: '127.0.0.1:7575',
      error: '',
      warnings: [],
      configWritable: true,
      activeOperations: [],
      operationRevision: 0
    });
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(store.externalScanning).toBe(false);

    resolveFinalStatus({
      running: true,
      address: '127.0.0.1:7575',
      error: '',
      warnings: [],
      configWritable: true,
      activeOperations: [{ id: 42, kind: 'auto-sleep' }],
      operationRevision: 1
    });
    await vi.waitFor(() => expect(store.externalOperationRunning).toBe(true));
    expect(store.externalScanning).toBe(false);
    expect(backend.ScanAndFetchStations).not.toHaveBeenCalled();
  });

  it('classifies a failed scan into a recovery error without a duplicate toast', async () => {
    backend.ScanAndFetchStations.mockRejectedValue(new Error('Bluetooth is unavailable; turn on Bluetooth and retry'));
    backend.GetCurrentStationInfo.mockResolvedValue([]);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.scanError).not.toBeNull());
    expect(store.scanError?.kind).toBe('bluetooth-off');
    // The persistent recovery card carries the detail; the status line keeps
    // a short summary and no toast duplicates the failure.
    expect(store.statusMessage).toBe('Scan failed: Bluetooth is unavailable');
    expect(pushToast).not.toHaveBeenCalledWith(expect.stringContaining('Scan failed'));
  });

  it('keeps a short status line for unknown scan failures', async () => {
    backend.ScanAndFetchStations.mockRejectedValue(new Error('mystery backend failure'));
    backend.GetCurrentStationInfo.mockResolvedValue([]);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.scanError?.kind).toBe('unknown'));
    expect(store.statusMessage).toBe('Scan failed.');
    expect(store.scanError?.detail).toBe('mystery backend failure');
  });
});

describe('StationStore station list', () => {
  it('reuses station object identity for unchanged list entries across scans', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    const first = store.stations[0];

    backend.ScanAndFetchStations.mockResolvedValue([createStation()]);
    await store.startScan();

    expect(store.stations).toHaveLength(1);
    expect(store.stations[0]).toBe(first);
  });

  it('rejects a station list committed against a stale revision', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    expect(store.applyStationList([createStation({ name: 'LHB-STALE' })], 999)).toBe(false);
    expect(store.stations[0].name).toBe('LHB-TEST');
  });
});

describe('StationStore power operations', () => {
  it('reports confirmed power changes through feedback, status and busy state', async () => {
    const updated = createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b });
    backend.SetStationPower.mockResolvedValue({
      station: updated, commandSent: true, confirmed: true, confirmationError: ''
    });
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    await store.setPower(store.stations[0], 'on');

    expect(backend.SetStationPower).toHaveBeenCalledWith('11:22:33:44:55:66', 'on');
    expect(store.powerFeedbackMap['11:22:33:44:55:66']?.kind).toBe('success');
    expect(store.powerFeedbackMap['11:22:33:44:55:66']?.text).toBe('On confirmed');
    expect(store.statusMessage).toBe('LHB-TEST is On.');
    expect(store.gattOperations.size).toBe(0);
    expect(store.powerTargetByAddress['11:22:33:44:55:66']).toBeUndefined();
  });

  it('ignores a second power request while the address is busy', async () => {
    let resolvePower!: (value: unknown) => void;
    backend.SetStationPower.mockReturnValue(new Promise((resolve) => { resolvePower = resolve; }));
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    const station = store.stations[0];

    void store.setPower(station, 'on');
    await vi.waitFor(() => expect(backend.SetStationPower).toHaveBeenCalledOnce());
    void store.setPower(station, 'on');
    await Promise.resolve();

    expect(backend.SetStationPower).toHaveBeenCalledOnce();
    resolvePower({ station: createStation(), commandSent: true, confirmed: true, confirmationError: '' });
  });

  it('retires settled power feedback after a capability refresh returns a newer read', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    const oldRead = '2026-08-11T00:00:00Z';
    const newRead = '2026-08-11T00:00:01Z';
    store.powerFeedback.set(store.stations[0].address, {
      kind: 'success', text: 'Sleep confirmed', target: 'sleep', readAt: oldRead
    });
    backend.RefreshStationCapabilities.mockResolvedValue(
      createStation({ lastPowerReadAt: newRead })
    );

    await store.refreshCapabilities(store.stations[0]);

    expect(store.powerFeedbackMap[store.stations[0].address]).toBeUndefined();
  });
});

describe('StationStore bulk power', () => {
  it('demands UI confirmation when part of the fleet is untrusted', async () => {
    backend.ScanAndFetchStations.mockResolvedValue([
      createStation({ name: 'LHB-TRUSTED', address: 'AA' }),
      createStation({ name: 'LHB-STALE', address: 'BB', isPresent: false })
    ]);
    const { store, ui } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(2));

    store.requestBulkPower('on');

    expect(ui.requestBulkConfirmation).toHaveBeenCalledWith('on');
    expect(backend.SetAllStationsPowerDetailed).not.toHaveBeenCalled();
  });

  it('runs directly for a fully trusted fleet', async () => {
    backend.SetAllStationsPowerDetailed.mockResolvedValue({ target: 'on', results: [] });
    const { store, ui } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    store.requestBulkPower('on');

    await vi.waitFor(() => expect(backend.SetAllStationsPowerDetailed).toHaveBeenCalledWith('on'));
    expect(ui.requestBulkConfirmation).not.toHaveBeenCalled();
    await vi.waitFor(() => expect(store.bulkTarget).toBeNull());
    expect(store.globalOperation).toBe('idle');
  });

  it('keeps new bulk commands locked until a pending cancellation request settles', async () => {
    const bulk = deferred<{ target: string; results: never[] }>();
    const cancellation = deferred<void>();
    backend.SetAllStationsPowerDetailed.mockReturnValueOnce(bulk.promise);
    backend.CancelBulkPower.mockReturnValueOnce(cancellation.promise);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    const runningBulk = store.runBulkPower('on');
    await vi.waitFor(() => expect(backend.SetAllStationsPowerDetailed).toHaveBeenCalledOnce());
    const pendingCancellation = store.cancelBulkPower();
    await vi.waitFor(() => expect(backend.CancelBulkPower).toHaveBeenCalledOnce());

    bulk.resolve({ target: 'on', results: [] });
    await runningBulk;

    expect(store.globalOperation).toBe('idle');
    expect(store.cancellingBulk).toBe(true);
    expect(store.bulkLocked).toBe(true);

    await store.runBulkPower('sleep');
    expect(backend.SetAllStationsPowerDetailed).toHaveBeenCalledOnce();

    cancellation.resolve(undefined);
    await pendingCancellation;
    expect(store.cancellingBulk).toBe(false);
    expect(store.bulkLocked).toBe(false);
  });
});

describe('StationStore fleet aggregates', () => {
  it('hides the bulk On thumb while stations are only heuristically on', async () => {
    // Backend boot fallback marks decoded-on with booting raw values as
    // confirmed; the bulk bar must not light up for those.
    backend.ScanAndFetchStations.mockResolvedValue([
      createStation({ name: 'LHB-A', address: 'AA', powerState: 1, powerStateName: 'on', rawPowerState: 0x01 }),
      createStation({ name: 'LHB-B', address: 'BB', powerState: 1, powerStateName: 'on', rawPowerState: 0x08 })
    ]);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(2));
    expect(store.allOn).toBe(false);
    expect(store.fleetOn).toBe(0);
    expect(store.fleetUnverified).toBe(2);
  });

  it('shows the bulk On thumb once every station reports a stable on raw', async () => {
    backend.ScanAndFetchStations.mockResolvedValue([
      createStation({ name: 'LHB-A', address: 'AA', powerState: 1, powerStateName: 'on', rawPowerState: 0x09 }),
      createStation({ name: 'LHB-B', address: 'BB', powerState: 1, powerStateName: 'on', rawPowerState: 0x0b })
    ]);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(2));
    expect(store.allOn).toBe(true);
    expect(store.fleetOn).toBe(2);
    expect(store.fleetUnverified).toBe(0);
  });
});

describe('StationStore channel editor', () => {
  it('closes the editor through the UI bridge on a confirmed channel change', async () => {
    const updated = createStation({ channel: 4, channelFresh: true });
    backend.SetStationChannel.mockResolvedValue({
      address: updated.address, previousChannel: 3, channel: 4, commandSent: true,
      confirmed: true, confirmationError: '', warnings: [], station: updated
    });
    const { store, ui } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    vi.mocked(ui.forceCloseChannelEditor).mockClear();

    await store.saveChannel(store.stations[0], 4, false);

    expect(backend.SetStationChannel).toHaveBeenCalledWith('11:22:33:44:55:66', 4, false);
    // The save's own completion force-closes even though the station is still
    // busy until the finally block releases the flags.
    expect(ui.forceCloseChannelEditor).toHaveBeenCalledOnce();
    expect(store.channelError).toBe('');
    expect(store.channelWarning).toBe(false);
    expect(store.statusMessage).toContain('Channel changed from 3 to 4.');
  });

  it('keeps the editor open and surfaces an unconfirmed channel result', async () => {
    // A station without a current channel avoids the same-channel early-out;
    // the post-write readback then comes from the authoritative list refresh.
    backend.ScanAndFetchStations.mockResolvedValue([createStation({ channel: 0 })]);
    backend.SetStationChannel.mockResolvedValue({
      previousChannel: 0, channel: 3, commandSent: true, confirmed: false,
      confirmationError: 'readback timed out', warnings: []
    });
    const { store, ui } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    vi.mocked(ui.closeChannelEditor).mockClear();

    await store.saveChannel(store.stations[0], 3, false);

    expect(backend.SetStationChannel).toHaveBeenCalledWith('11:22:33:44:55:66', 3, false);
    expect(ui.closeChannelEditor).not.toHaveBeenCalled();
    expect(store.channelWarning).toBe(true);
    expect(store.channelError).toContain('Channel command sent but unconfirmed: readback timed out');
    expect(store.channelError).toContain('Readback: actual 3');
  });
});

describe('StationStore overlay coordination', () => {
  it('clears rename and overlay layers when a new scan starts', async () => {
    const { store, ui } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    vi.mocked(ui.closeChannelEditor).mockClear();
    vi.mocked(ui.clearBulkConfirmation).mockClear();
    store.startRename(store.stations[0]);
    expect(store.editingAddress).toBe('11:22:33:44:55:66');

    await store.startScan();

    expect(store.editingAddress).toBeNull();
    expect(ui.closeChannelEditor).toHaveBeenCalled();
    expect(ui.clearBulkConfirmation).toHaveBeenCalled();
  });
});

describe('StationStore auto sleep events', () => {
  it('toasts auto-sleep lifecycle events', async () => {
    mountStore();
    await vi.waitFor(() => expect(runtime.handlers.has('auto-sleep')).toBe(true));

    runtime.handlers.get('auto-sleep')?.({ phase: 'started' });
    expect(pushToast).toHaveBeenCalledWith('Session ended — scanning and putting all stations to sleep.', 'info');
    expect(store?.autoSleepRunning).toBe(true);
    expect(store?.scanLocked).toBe(true);
    expect(store?.bulkLocked).toBe(true);
    expect(store?.stationLocked).toBe(true);

    runtime.handlers.get('auto-sleep')?.({
      phase: 'completed', success: 2, unconfirmed: 0, failed: 0,
      updateId: 1,
      stations: [createStation({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x00 })]
    });
    expect(pushToast).toHaveBeenCalledWith('Auto sleep finished: 2 station(s) put to sleep.', 'success');
    expect(store?.stations[0].powerStateName).toBe('sleep');
    expect(store?.autoSleepRunning).toBe(false);

    runtime.handlers.get('auto-sleep')?.({
      phase: 'completed', success: 1, unconfirmed: 0, failed: 0, skipped: 1, updateId: 2,
      stations: [createStation({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x00 })]
    });
    expect(pushToast).toHaveBeenCalledWith('Auto sleep finished: 1 confirmed, 0 unconfirmed, 0 failed, 1 skipped.', 'warning');

    runtime.handlers.get('auto-sleep')?.({
      phase: 'completed', success: 0, unconfirmed: 1, failed: 0, skipped: 0, updateId: 3,
      stations: [createStation({ powerStateConfirmed: false })]
    });
    expect(pushToast).toHaveBeenCalledWith('Auto sleep finished: 0 confirmed, 1 unconfirmed, 0 failed, 0 skipped.', 'warning');
  });

  it('reports partial results when automatic sleep is cancelled', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(runtime.handlers.has('auto-sleep')).toBe(true));

    runtime.handlers.get('auto-sleep')?.({ phase: 'started' });
    runtime.handlers.get('auto-sleep')?.({
      phase: 'cancelled', success: 1, unconfirmed: 1, failed: 0, skipped: 2, updateId: 1,
      stations: [createStation({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x00 })]
    });

    expect(store.autoSleepRunning).toBe(false);
    expect(store.statusMessage).toBe('Auto sleep cancelled: 1 confirmed, 1 unconfirmed, 0 failed, 2 skipped.');
    expect(pushToast).toHaveBeenCalledWith(
      'Auto sleep cancelled: 1 confirmed, 1 unconfirmed, 0 failed, 2 skipped.',
      'warning'
    );
    expect(store.stations[0].powerStateName).toBe('sleep');
  });

  it('does not start periodic status work while automatic sleep is active', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    backend.CheckAllStationStatuses.mockClear();

    runtime.handlers.get('auto-sleep')?.({ phase: 'started' });
    await (store as unknown as { periodicStatusCheck(): Promise<void> }).periodicStatusCheck();

    expect(backend.CheckAllStationStatuses).not.toHaveBeenCalled();
  });
});

describe('StationStore external HTTP operation events', () => {
  it('recovers an operation that started before event listeners mounted', async () => {
    backend.GetAPIStatus.mockResolvedValue({
      running: true,
      address: '127.0.0.1:7575',
      error: '',
      warnings: [],
      configWritable: true,
      activeOperations: [{ id: 73, kind: 'bulk-power' }],
      operationRevision: 4
    });
    const { store } = mountStore();

    await vi.waitFor(() => expect(store.externalOperationRunning).toBe(true));
    expect(store.scanLocked).toBe(true);
    expect(store.bulkLocked).toBe(true);
    expect(store.stationLocked).toBe(true);
  });

  it('does not let an older operation snapshot clear a newer event', async () => {
    let resolveStatus!: (value: Record<string, unknown>) => void;
    backend.GetAPIStatus.mockReturnValue(new Promise((resolve) => { resolveStatus = resolve; }));
    const { store } = mountStore();
    await vi.waitFor(() => expect(runtime.handlers.has('external-operation')).toBe(true));

    runtime.handlers.get('external-operation')?.({ id: 91, phase: 'started', kind: 'power', revision: 2 });
    resolveStatus({
      running: true,
      address: '127.0.0.1:7575',
      error: '',
      warnings: [],
      configWritable: true,
      activeOperations: [],
      operationRevision: 0
    });

    await Promise.resolve();
    expect(store.externalOperationRunning).toBe(true);
  });


  it('locks controls until every overlapping external operation finishes', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(runtime.handlers.has('external-operation')).toBe(true));
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    runtime.handlers.get('external-operation')?.({ id: 11, phase: 'started', kind: 'power' });
    runtime.handlers.get('external-operation')?.({ id: 12, phase: 'started', kind: 'identify' });

    expect(store.externalOperationRunning).toBe(true);
    expect(store.scanLocked).toBe(true);
    expect(store.bulkLocked).toBe(true);
    expect(store.stationLocked).toBe(true);

    runtime.handlers.get('external-operation')?.({ id: 11, phase: 'finished', kind: 'power' });
    expect(store.externalOperationRunning).toBe(true);

    runtime.handlers.get('external-operation')?.({ id: 12, phase: 'finished', kind: 'identify' });
    expect(store.externalOperationRunning).toBe(false);
    expect(store.scanLocked).toBe(false);
    expect(store.bulkLocked).toBe(false);
    expect(store.stationLocked).toBe(false);
  });

  it('skips periodic status checks while an external operation is running', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(runtime.handlers.has('external-operation')).toBe(true));
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    runtime.handlers.get('external-operation')?.({ id: 21, phase: 'started', kind: 'bulk-power' });
    await vi.waitFor(() => expect(store.externalOperationRunning).toBe(true));

    // The backend rejects concurrent status refreshes while an external
    // operation holds the global lock; the poller must not even ask.
    backend.CheckAllStationStatuses.mockClear();
    await (store as unknown as { periodicStatusCheck(): Promise<void> }).periodicStatusCheck();
    expect(backend.CheckAllStationStatuses).not.toHaveBeenCalled();

    runtime.handlers.get('external-operation')?.({ id: 21, phase: 'finished', kind: 'bulk-power' });
    await vi.waitFor(() => expect(store.externalOperationRunning).toBe(false));
    await (store as unknown as { periodicStatusCheck(): Promise<void> }).periodicStatusCheck();
    expect(backend.CheckAllStationStatuses).toHaveBeenCalled();
  });

  it('abandons a periodic status check when an external operation starts mid-check', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(runtime.handlers.has('external-operation')).toBe(true));
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    let resolveScanning!: (scanning: boolean) => void;
    backend.IsScanning.mockReturnValueOnce(new Promise((resolve) => { resolveScanning = resolve; }));
    backend.CheckAllStationStatuses.mockClear();

    const pending = (store as unknown as { periodicStatusCheck(): Promise<void> }).periodicStatusCheck();
    await vi.waitFor(() => expect(backend.IsScanning).toHaveBeenCalled());
    runtime.handlers.get('external-operation')?.({ id: 22, phase: 'started', kind: 'power', revision: 2 });
    resolveScanning(false);
    await pending;

    expect(backend.CheckAllStationStatuses).not.toHaveBeenCalled();
    expect(store.globalOperation).toBe('idle');
  });

  it('does not discard an accepted local scan when a delayed HTTP snapshot arrives', async () => {
    let resolveScan!: (stations: ReturnType<typeof createStation>[]) => void;
    backend.ScanAndFetchStations.mockReturnValueOnce(new Promise((resolve) => { resolveScan = resolve; }));
    const { store } = mountStore();
    await vi.waitFor(() => expect(backend.ScanAndFetchStations).toHaveBeenCalledOnce());

    // A cached projection read may have started just before the scan. The
    // rejected HTTP request must not use that older read as a reason to
    // invalidate the scan revision that now owns the authoritative result.
    (store as unknown as { projectionRefreshInFlight: boolean }).projectionRefreshInFlight = true;

    runtime.handlers.get('external-operation')?.({ id: 23, phase: 'started', kind: 'power', revision: 2 });
    runtime.handlers.get('external-stations-updated')?.({
      id: 23,
      source: 'http-power',
      stations: [createStation({ name: 'LHB-DELAYED-HTTP-SNAPSHOT' })]
    });
    runtime.handlers.get('external-operation')?.({ id: 23, phase: 'finished', kind: 'power', revision: 3 });
    (store as unknown as { projectionRefreshInFlight: boolean }).projectionRefreshInFlight = false;
    resolveScan([createStation({ name: 'LHB-SCAN-RESULT' })]);

    await vi.waitFor(() => expect(store.globalOperation).toBe('idle'));
    expect(store.stations).toHaveLength(1);
    expect(store.stations[0].name).toBe('LHB-SCAN-RESULT');
  });
});

describe('StationStore external station updates', () => {
  it('applies the newest HTTP snapshot and ignores older event ids', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    await vi.waitFor(() => expect(runtime.handlers.has('external-stations-updated')).toBe(true));

    runtime.handlers.get('external-stations-updated')?.({
      id: 2,
      source: 'http-power',
      stations: [createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b })]
    });
    expect(store.stations[0].powerStateName).toBe('on');

    runtime.handlers.get('external-stations-updated')?.({
      id: 1,
      source: 'http-power',
      stations: [createStation({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x00 })]
    });
    expect(store.stations[0].powerStateName).toBe('on');
  });

  it('invalidates a pending status response when a newer HTTP event arrives', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    let resolveStatus!: (stations: ReturnType<typeof createStation>[]) => void;
    backend.CheckAllStationStatuses.mockReturnValue(new Promise((resolve) => { resolveStatus = resolve; }));
    const pendingCheck = (store as unknown as { periodicStatusCheck(): Promise<void> }).periodicStatusCheck();
    await vi.waitFor(() => expect(backend.CheckAllStationStatuses).toHaveBeenCalledOnce());

    runtime.handlers.get('external-stations-updated')?.({
      id: 5,
      source: 'http-power',
      stations: [createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b })]
    });
    expect(store.stations[0].powerStateName).toBe('on');

    resolveStatus([createStation({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x00 })]);
    await pendingCheck;
    expect(store.stations[0].powerStateName).toBe('on');
  });

  it('orders automatic-sleep snapshots with HTTP updates using the shared id', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));

    runtime.handlers.get('external-stations-updated')?.({
      id: 8,
      source: 'http-power',
      stations: [createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b })]
    });
    runtime.handlers.get('auto-sleep')?.({
      phase: 'completed', success: 1, failed: 0, updateId: 7,
      stations: [createStation({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x00 })]
    });

    expect(store.stations[0].powerStateName).toBe('on');
  });
});
