import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  CheckAllStationStatuses: vi.fn(),
  GetAPIStatus: vi.fn(),
  GetCurrentStationInfo: vi.fn(),
  GetScanStatus: vi.fn(),
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

function createUi(): StationStoreUi {
  return {
    closeChannelEditor: vi.fn(),
    clearBulkConfirmation: vi.fn(),
    requestBulkConfirmation: vi.fn()
  };
}

let store: StationStore | null = null;

function mountStore(ui = createUi()) {
  store = new StationStore(ui);
  store.mount();
  return { store, ui };
}

beforeEach(() => {
  vi.clearAllMocks();
  runtime.handlers.clear();
  backend.IsScanning.mockResolvedValue(false);
  backend.GetAPIStatus.mockResolvedValue({
    running: true, address: '127.0.0.1:7575', error: '', warnings: [], configWritable: true
  });
  backend.ScanAndFetchStations.mockResolvedValue([createStation()]);
  backend.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
  backend.GetCurrentStationInfo.mockResolvedValue([createStation()]);
  backend.CheckAllStationStatuses.mockResolvedValue([createStation()]);
  backend.StopScan.mockResolvedValue(undefined);
  backend.SetStationPower.mockResolvedValue({
    station: createStation(), commandSent: true, confirmed: true, confirmationError: ''
  });
});

afterEach(() => {
  store?.dispose();
  store = null;
});

describe('StationStore startup', () => {
  it('runs the initial scan on mount when the backend is idle', async () => {
    const { store } = mountStore();
    await vi.waitFor(() => expect(backend.ScanAndFetchStations).toHaveBeenCalledOnce());
    await vi.waitFor(() => expect(store.stations).toHaveLength(1));
    expect(store.stations[0].name).toBe('LHB-TEST');
    expect(store.statusMessage).toBe('Found 1; 1 known station.');
    expect(store.scanError).toBeNull();
  });

  it('adopts a running backend scan instead of starting a local scan', async () => {
    backend.IsScanning.mockResolvedValue(true);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.externalScanning).toBe(true));
    expect(backend.ScanAndFetchStations).not.toHaveBeenCalled();
  });

  it('classifies a failed scan into a recovery error and toasts it', async () => {
    backend.ScanAndFetchStations.mockRejectedValue(new Error('Bluetooth is unavailable; turn on Bluetooth and retry'));
    backend.GetCurrentStationInfo.mockResolvedValue([]);
    const { store } = mountStore();
    await vi.waitFor(() => expect(store.scanError).not.toBeNull());
    expect(store.scanError?.kind).toBe('bluetooth-off');
    expect(store.statusMessage).toContain('Scan failed:');
    expect(pushToast).toHaveBeenCalledWith(expect.stringContaining('Scan failed:'));
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
    vi.mocked(ui.closeChannelEditor).mockClear();

    await store.saveChannel(store.stations[0], 4, false);

    expect(backend.SetStationChannel).toHaveBeenCalledWith('11:22:33:44:55:66', 4, false);
    expect(ui.closeChannelEditor).toHaveBeenCalledOnce();
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

    runtime.handlers.get('auto-sleep')?.({ phase: 'completed', success: 2, failed: 0 });
    expect(pushToast).toHaveBeenCalledWith('Auto sleep finished: 2 station(s) put to sleep.', 'success');
  });
});
