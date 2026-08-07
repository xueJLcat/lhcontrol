import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from './lib/types';
import { pushToast } from './lib/toast';

const api = vi.hoisted(() => ({
  CheckAllStationStatuses: vi.fn(),
  CancelBulkPower: vi.fn(),
  GetAPIStatus: vi.fn(),
  GetAutoSleepSettings: vi.fn(),
  GetScanOnStartup: vi.fn(),
  GetStatusPollIntervalSeconds: vi.fn(),
  GetStatusPollingEnabled: vi.fn(),
  GetCurrentStationInfo: vi.fn(),
  GetScanStatus: vi.fn(),
  IdentifyStation: vi.fn(),
  IsScanning: vi.fn(),
  ListBluetoothAdapters: vi.fn(),
  RefreshStationCapabilities: vi.fn(),
  RenameStationByAddress: vi.fn(),
  ScanAndFetchStations: vi.fn(),
  SetAllStationsPowerDetailed: vi.fn(),
  SetAutoSleepSettings: vi.fn(),
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

vi.mock('../wailsjs/go/main/App', () => api);
vi.mock('../wailsjs/runtime/runtime', () => ({ EventsOn: runtime.EventsOn }));
vi.mock('./lib/toast', async (importOriginal) => {
  const original = await importOriginal<typeof import('./lib/toast')>();
  return { ...original, pushToast: vi.fn() };
});

import App from './App.svelte';
import { createStation } from './test/fixtures';

function externalScanEvent(id: number, overrides: Partial<{ stations: StationInfo[]; error: string }> = {}) {
  return { id, ...overrides };
}

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    value: vi.fn(() => {
      const animation = {
        cancel: vi.fn(),
        finished: Promise.resolve(),
        set onfinish(callback: ((event: AnimationPlaybackEvent) => void) | null) {
          if (callback) Promise.resolve().then(() => callback({} as AnimationPlaybackEvent));
        }
      };
      return animation;
    })
  });
  runtime.handlers.clear();
  api.GetAPIStatus.mockResolvedValue({
    running: true,
    address: '127.0.0.1:7575',
    error: '',
    warnings: [],
    configWritable: true
  });
  api.IsScanning.mockResolvedValue(false);
  api.GetScanOnStartup.mockResolvedValue(true);
  api.GetStatusPollIntervalSeconds.mockResolvedValue(15);
  api.GetStatusPollingEnabled.mockResolvedValue(true);
  api.ScanAndFetchStations.mockResolvedValue([createStation()]);
  api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
  api.GetCurrentStationInfo.mockResolvedValue([createStation()]);
  api.CheckAllStationStatuses.mockResolvedValue([createStation()]);
  api.StopScan.mockResolvedValue(undefined);
  api.ListBluetoothAdapters.mockResolvedValue([]);
  api.GetAutoSleepSettings.mockResolvedValue({ enabled: false, target: 'steamvr', delaySeconds: 300 });
  api.SetAutoSleepSettings.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('App asynchronous operations', () => {
  it('shows per-station feedback for a successful bulk command', async () => {
    const updated = createStation({
      powerState: 1,
      powerStateName: 'on',
      rawPowerState: 0x0b
    });
    api.SetAllStationsPowerDetailed.mockResolvedValue({
      target: 'on',
      results: [{
        address: updated.address,
        name: updated.name,
        skipped: false,
        reason: '',
        commandSent: true,
        success: true,
        confirmed: true,
        error: '',
        station: updated
      }]
    });
    api.GetCurrentStationInfo.mockResolvedValue([updated]);

    render(App);
    await screen.findByText('LHB-TEST');
    const bulkOn = await screen.findByTitle('Turn all known stations on');
    await fireEvent.click(bulkOn);
    await waitFor(() => expect(api.SetAllStationsPowerDetailed).toHaveBeenCalledWith('on'));
    expect(await screen.findByText('On confirmed')).toBeInTheDocument();
  });

  it('adds backend-known bulk result stations when the follow-up list refresh fails', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    api.ScanAndFetchStations.mockResolvedValue([stationA]);
    api.GetCurrentStationInfo.mockRejectedValue(new Error('fixture list failure'));
    api.SetAllStationsPowerDetailed.mockResolvedValue({
      target: 'on',
      results: [
        {
          address: 'AA', name: 'LHB-A', skipped: false, reason: '',
          commandSent: true, success: true, confirmed: true, error: '',
          station: { ...stationA, powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }
        },
        {
          address: 'BB', name: 'LHB-B', skipped: false, reason: '',
          commandSent: true, success: true, confirmed: true, error: '',
          station: { ...stationB, powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }
        }
      ]
    });

    render(App);
    await screen.findByText('LHB-A');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByTitle('Turn all known stations on'));

    expect(await screen.findByText('LHB-B')).toBeInTheDocument();
    await waitFor(() => expect(screen.getAllByText('On confirmed')).toHaveLength(2));
  });

  it('keeps the authoritative follow-up snapshot over older bulk result snapshots', async () => {
    const initial = createStation({ name: 'LHB-A', address: 'AA' });
    const latest = createStation({
      name: 'LHB-A', address: 'AA', powerState: 1, powerStateName: 'on', rawPowerState: 0x0b
    });
    api.ScanAndFetchStations.mockResolvedValue([initial]);
    api.GetCurrentStationInfo.mockResolvedValue([latest]);
    api.SetAllStationsPowerDetailed.mockResolvedValue({
      target: 'on',
      results: [{
        address: 'AA', name: 'LHB-A', skipped: false, reason: '',
        commandSent: true, success: true, confirmed: true, error: '',
        // Simulate a worker snapshot captured before a newer backend refresh.
        station: initial
      }]
    });

    render(App);
    await screen.findByText('LHB-A');
    await fireEvent.click(await screen.findByTitle('Turn all known stations on'));

    await waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-A on' }))
      .toHaveAttribute('aria-pressed', 'true'));
  });

  it('locks rename while a bulk operation is pending', async () => {
    let resolveBulk!: (value: unknown) => void;
    api.SetAllStationsPowerDetailed.mockReturnValue(new Promise((resolve) => {
      resolveBulk = resolve;
    }));
    render(App);
    await screen.findByText('LHB-TEST');
    await screen.findByRole('button', { name: 'Scan' });

    await fireEvent.click(screen.getByTitle('Turn all known stations on'));
    await waitFor(() => expect(api.SetAllStationsPowerDetailed).toHaveBeenCalledOnce());
    expect(screen.getByRole('button', { name: 'Rename LHB-TEST' })).toBeDisabled();

    resolveBulk({ target: 'on', results: [] });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Rename LHB-TEST' })).not.toBeDisabled());
  });

  it('cancels an active bulk operation and reports its partial result', async () => {
    let resolveBulk!: (value: unknown) => void;
    api.SetAllStationsPowerDetailed.mockReturnValue(new Promise((resolve) => {
      resolveBulk = resolve;
    }));
    api.CancelBulkPower.mockResolvedValue(undefined);
    const station = createStation();
    render(App);
    await screen.findByText('LHB-TEST');

    await fireEvent.click(await screen.findByTitle('Turn all known stations on'));
    const cancel = await screen.findByRole('button', { name: 'Cancel bulk power' });
    await fireEvent.click(cancel);
    await waitFor(() => expect(api.CancelBulkPower).toHaveBeenCalledOnce());

    resolveBulk({
      target: 'on',
      cancelled: true,
      timedOut: false,
      results: [{
        address: station.address, name: station.name, skipped: true,
        reason: 'operation cancelled', commandSent: false, success: false,
        confirmed: false, error: '', station
      }]
    });
    await waitFor(() => expect(pushToast).toHaveBeenCalledWith(
      expect.stringContaining('Bulk On cancelled'), 'warning'
    ));
  });

  it('disables a third GATT operation until one of two active operations finishes', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    const stationC = createStation({ name: 'LHB-C', address: 'CC' });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB, stationC]);

    const resolvers = new Map<string, (value: unknown) => void>();
    api.SetStationPower.mockImplementation((address: string) => new Promise((resolve) => {
      resolvers.set(address, resolve);
    }));

    render(App);
    await screen.findByText('LHB-C');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    const onButtons = screen.getAllByTitle('Turn lasers and motor on');
    await fireEvent.click(onButtons[0]);
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledTimes(1));
    await fireEvent.click(onButtons[1]);
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledTimes(2));
    expect(onButtons[2]).toBeDisabled();

    resolvers.get('AA')?.({
      station: { ...stationA, powerState: 1, powerStateName: 'on' },
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    await waitFor(() => expect(onButtons[2]).not.toBeDisabled());
  });

  it('commits successful results from two different stations independently', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB]);
    const resolvers = new Map<string, (value: unknown) => void>();
    api.SetStationPower.mockImplementation((address: string) => new Promise((resolve) => {
      resolvers.set(address, resolve);
    }));

    render(App);
    await screen.findByText('LHB-B');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-A on' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-B on' }));
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledTimes(2));

    resolvers.get('BB')?.({
      station: { ...stationB, powerState: 1, powerStateName: 'on', rawPowerState: 0x0b },
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    resolvers.get('AA')?.({
      station: { ...stationA, powerState: 1, powerStateName: 'on', rawPowerState: 0x0b },
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });

    await waitFor(() => expect(screen.getAllByText('On confirmed')).toHaveLength(2));
  });

  it('does not let an earlier station operation overwrite a later footer status', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB]);
    const resolvers = new Map<string, (value: unknown) => void>();
    api.SetStationPower.mockImplementation((address: string) => new Promise((resolve) => {
      resolvers.set(address, resolve);
    }));

    render(App);
    await screen.findByText('LHB-B');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-A on' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-B on' }));
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledTimes(2));

    resolvers.get('BB')?.({ station: { ...stationB, powerState: 1, powerStateName: 'on' }, confirmed: true });
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('LHB-B is On.'));
    resolvers.get('AA')?.({ station: { ...stationA, powerState: 1, powerStateName: 'on' }, confirmed: true });

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('LHB-B is On.'));
  });

  it('does not let external recovery polling overwrite a newer station footer', async () => {
    vi.useFakeTimers();
    const station = createStation();
    let resolveRecoveryList!: (stations: StationInfo[]) => void;
    api.IsScanning.mockResolvedValue(false);
    api.GetCurrentStationInfo.mockReturnValueOnce(new Promise((resolve) => {
      resolveRecoveryList = resolve;
    }));
    api.SetStationPower.mockResolvedValue({
      station: { ...station, powerState: 1, powerStateName: 'on' }, confirmed: true
    });
    api.GetScanStatus.mockResolvedValue({ state: 'failed', found: 0, error: 'old external failure', warnings: [] });

    render(App);
    await screen.findByText('LHB-TEST');
    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(1, { error: 'old external failure' }));
    await waitFor(() => expect(api.GetCurrentStationInfo).toHaveBeenCalledOnce());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    expect(await screen.findByRole('status')).toHaveTextContent('LHB-TEST is On.');

    resolveRecoveryList([station]);
    await vi.advanceTimersByTimeAsync(15_000);
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('LHB-TEST is On.'));
    expect(screen.queryByText(/External scan failed/)).not.toBeInTheDocument();
  });

  it('keeps a failed station result when another concurrent operation advances the list revision', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    const updatedB = createStation({
      name: 'LHB-B',
      address: 'BB',
      powerState: 1,
      powerStateName: 'on'
    });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB]);

    let rejectA!: (error: Error) => void;
    let resolveB!: (value: unknown) => void;
    let resolveRefresh!: (stations: StationInfo[]) => void;
    api.SetStationPower.mockImplementation((address: string) => {
      if (address === 'AA') {
        return new Promise((_, reject) => { rejectA = reject; });
      }
      return new Promise((resolve) => { resolveB = resolve; });
    });
    api.GetCurrentStationInfo.mockReturnValue(new Promise((resolve) => {
      resolveRefresh = resolve;
    }));

    render(App);
    await screen.findByText('LHB-B');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-A on' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-B on' }));

    rejectA(new Error('connection lost'));
    await waitFor(() => expect(api.GetCurrentStationInfo).toHaveBeenCalledOnce());
    resolveB({
      station: updatedB,
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    expect(await screen.findByText('On confirmed')).toBeInTheDocument();

    resolveRefresh([stationA, updatedB]);
    expect(await screen.findByText('Failed · actual sleep')).toBeInTheDocument();
    expect(screen.queryByText('Switching to On…')).not.toBeInTheDocument();
  });

  it('does not label cached power as actual when failed readback is unavailable', async () => {
    api.SetStationPower.mockRejectedValue(new Error('connection lost'));
    api.GetCurrentStationInfo.mockRejectedValue(new Error('readback failed'));
    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    expect(await screen.findByText('Failed · unavailable')).toBeInTheDocument();
    expect(screen.queryByText('Failed · actual sleep')).not.toBeInTheDocument();
  });

  it('labels stale power readback as last-known after a failed command', async () => {
    api.SetStationPower.mockRejectedValue(new Error('connection lost'));
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ powerFresh: false })]);
    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    expect(await screen.findByText('Failed · last-known sleep')).toBeInTheDocument();
  });

  it('reports a backend no-op power result as already at target', async () => {
    const alreadyOn = createStation({
      powerState: 1,
      powerStateName: 'on',
      rawPowerState: 0x0b,
      lastPowerReadAt: '2026-07-29T08:00:00Z'
    });
    api.SetStationPower.mockResolvedValue({
      station: alreadyOn,
      commandSent: false,
      skipped: true,
      reason: 'already at target state',
      confirmed: true,
      confirmationError: ''
    });
    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    expect(await screen.findByText('Already On')).toBeInTheDocument();
    expect(await screen.findByRole('status')).toHaveTextContent('LHB-TEST is already On; no command was sent.');
    expect(api.SetStationPower).toHaveBeenCalledWith('11:22:33:44:55:66', 'on');
  });

  it('clears completed power feedback when a periodic read reports a different state', async () => {
    vi.useFakeTimers();
    const initial = createStation({ lastPowerReadAt: '2026-07-29T08:00:00Z' });
    const poweredOn = createStation({
      powerState: 1,
      powerStateName: 'on',
      rawPowerState: 0x0b,
      lastPowerReadAt: '2026-07-29T08:00:01Z'
    });
    api.ScanAndFetchStations.mockResolvedValue([initial]);
    api.SetStationPower.mockResolvedValue({
      station: poweredOn,
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    api.CheckAllStationStatuses.mockResolvedValue([
      createStation({ lastPowerReadAt: '2026-07-29T08:00:02Z' })
    ]);

    render(App);
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    await vi.waitFor(() => expect(screen.getByText('On confirmed')).toBeInTheDocument());

    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.CheckAllStationStatuses).toHaveBeenCalledOnce());
    await vi.waitFor(() => expect(screen.queryByText('On confirmed')).not.toBeInTheDocument());
  });

  it('keeps GATT capacity available during rename but locks scan and bulk', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB]);
    let resolveRename!: () => void;
    api.RenameStationByAddress.mockReturnValue(new Promise<void>((resolve) => {
      resolveRename = resolve;
    }));

    render(App);
    await screen.findByText('LHB-B');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Rename LHB-A' }));
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Renamed A' } });
    await fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(api.RenameStationByAddress).toHaveBeenCalledWith('AA', 'Renamed A'));

    expect(screen.getByRole('button', { name: 'Scan' })).toBeDisabled();
    expect(within(screen.getByLabelText('Set all known stations')).getByRole('button', { name: 'On' })).toBeDisabled();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-B on' })).not.toBeDisabled());

    api.GetCurrentStationInfo.mockResolvedValue([
      { ...stationA, name: 'Renamed A' },
      stationB
    ]);
    resolveRename();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
  });

});
