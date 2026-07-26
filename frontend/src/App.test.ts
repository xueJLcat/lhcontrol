import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from './lib/types';

const api = vi.hoisted(() => ({
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
  SetStationPower: vi.fn()
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

function createStation(overrides: Partial<StationInfo> = {}): StationInfo {
  return {
    name: 'LHB-TEST',
    originalName: 'LHB-TEST',
    address: '11:22:33:44:55:66',
    powerState: 0,
    powerStateName: 'sleep',
    powerStateConfirmed: true,
    rawPowerState: 0,
    channel: 3,
    channelConflict: false,
    isPresent: true,
    seenInLatestScan: true,
    scanFresh: true,
    missedScans: 0,
    lastSeenAt: '',
    lastReadAt: '',
    lastPowerReadAt: '',
    lastChannelReadAt: '',
    metadataReadAt: '',
    lastError: '',
    statusFresh: true,
    powerFresh: true,
    channelFresh: true,
    metadataFresh: false,
    connectionState: 'connected',
    capabilitiesKnown: true,
    capabilities: {
      powerRead: true,
      powerWrite: true,
      powerNotify: false,
      standby: true,
      channelRead: true,
      channelWrite: true,
      channelNotify: false,
      identify: true,
      deviceInformation: false
    },
    metadata: {
      manufacturer: '',
      model: '',
      serialNumber: '',
      hardwareRevision: '',
      firmwareRevision: ''
    },
    ...overrides
  } as StationInfo;
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
  api.GetAPIStatus.mockResolvedValue({ running: true, address: '127.0.0.1:7575', error: '' });
  api.IsScanning.mockResolvedValue(false);
  api.ScanAndFetchStations.mockResolvedValue([createStation()]);
  api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
  api.GetCurrentStationInfo.mockResolvedValue([createStation()]);
  api.CheckAllStationStatuses.mockResolvedValue([createStation()]);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('App asynchronous operations', () => {
  it('loads the initial scan and renders a known station', async () => {
    render(App);
    expect(await screen.findByText('LHB-TEST')).toBeInTheDocument();
    expect(api.ScanAndFetchStations).toHaveBeenCalledOnce();
  });

  it('shows a recoverable Bluetooth message when the initial scan fails', async () => {
    api.ScanAndFetchStations.mockRejectedValue(new Error('Bluetooth is unavailable; turn on Bluetooth and retry'));
    render(App);
    expect((await screen.findAllByText(/Scan failed:.*Bluetooth is unavailable/)).length).toBeGreaterThan(0);
  });

  it('does not run periodic status reads during an external scan', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(true);
    render(App);
    await vi.waitFor(() => expect(api.IsScanning).toHaveBeenCalled());
    await vi.advanceTimersByTimeAsync(30_000);
    expect(api.CheckAllStationStatuses).not.toHaveBeenCalled();
  });

  it('does not commit a pending scan result after unmount', async () => {
    let resolveScan!: (stations: StationInfo[]) => void;
    api.ScanAndFetchStations.mockReturnValue(new Promise((resolve) => {
      resolveScan = resolve;
    }));
    const view = render(App);
    await waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledOnce());
    view.unmount();
    resolveScan([createStation({ name: 'LHB-LATE' })]);
    await Promise.resolve();
    expect(api.GetScanStatus).not.toHaveBeenCalled();
  });

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

  it('closes the rename editor when an external scan starts', async () => {
    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Rename LHB-TEST' }));
    expect(screen.getByRole('textbox', { name: 'Station name' })).toBeInTheDocument();

    runtime.handlers.get('external-scan-started')?.();

    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Station name' })).not.toBeInTheDocument());
    expect(screen.getByText('External scan in progress...')).toBeInTheDocument();
  });

  it('closes the channel editor and clears device feedback when an external scan starts', async () => {
    api.SetStationPower.mockResolvedValue({
      station: createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }),
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    expect(await screen.findByText('On confirmed')).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await fireEvent.click(await screen.findByRole('button', { name: /Change Channel/ }));
    expect(screen.getByRole('dialog', { name: 'Change channel' })).toBeInTheDocument();

    runtime.handlers.get('external-scan-started')?.();

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Change channel' })).not.toBeInTheDocument());
    expect(screen.queryByText('On confirmed')).not.toBeInTheDocument();
    expect(screen.getByText('External scan in progress...')).toBeInTheDocument();
  });

  it('does not let a pending device result overwrite a newer external scan', async () => {
    let resolvePower!: (value: unknown) => void;
    api.SetStationPower.mockReturnValue(new Promise((resolve) => {
      resolvePower = resolve;
    }));

    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledOnce());

    runtime.handlers.get('external-scan-started')?.();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scanning...' })).toBeDisabled());
    expect(screen.getByText('External scan in progress...')).toBeInTheDocument();

    resolvePower({
      station: createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }),
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    await Promise.resolve();

    expect(screen.getByText('External scan in progress...')).toBeInTheDocument();
    expect(screen.queryByText('On confirmed')).not.toBeInTheDocument();
  });

  it('distinguishes already-at-target and unsupported bulk skips', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB]);
    api.SetAllStationsPowerDetailed.mockResolvedValue({
      target: 'on',
      results: [
        {
          address: 'AA', name: 'LHB-A', skipped: true, reason: 'already at target',
          commandSent: false, success: true, confirmed: true, error: '', station: stationA
        },
        {
          address: 'BB', name: 'LHB-B', skipped: true, reason: 'power control is not supported',
          commandSent: false, success: false, confirmed: false, error: '', station: stationB
        }
      ]
    });
    api.GetCurrentStationInfo.mockResolvedValue([stationA, stationB]);

    render(App);
    await screen.findByText('LHB-B');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByTitle('Turn all known stations on'));

    expect(await screen.findByText('Already On')).toBeInTheDocument();
    expect(await screen.findByText('Skipped · power control is not supported')).toBeInTheDocument();
  });

  it('shows the backend confirmation error for an unconfirmed bulk command', async () => {
    const station = createStation();
    api.SetAllStationsPowerDetailed.mockResolvedValue({
      target: 'on',
      results: [{
        address: station.address,
        name: station.name,
        skipped: false,
        reason: '',
        commandSent: true,
        success: true,
        confirmed: false,
        error: 'readback timed out',
        station
      }]
    });

    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByTitle('Turn all known stations on'));

    expect(await screen.findByText('On sent · readback timed out')).toBeInTheDocument();
  });

  it('locks the empty-state scan button while an external scan is running', async () => {
    api.ScanAndFetchStations.mockResolvedValue([]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 0, warnings: [] });

    render(App);
    expect(await screen.findByRole('button', { name: 'Scan Now' })).not.toBeDisabled();
    runtime.handlers.get('external-scan-started')?.();

    const scanningButtons = await screen.findAllByRole('button', { name: 'Scanning...' });
    expect(scanningButtons.length).toBeGreaterThan(0);
    for (const button of scanningButtons) expect(button).toBeDisabled();
  });

  it('does not let an older API status response overwrite a newer poll', async () => {
    vi.useFakeTimers();
    let resolveFirst!: (value: unknown) => void;
    api.GetAPIStatus
      .mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve; }))
      .mockResolvedValueOnce({ running: false, address: '127.0.0.1:7575', error: 'new failure' });

    render(App);
    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.GetAPIStatus).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(screen.getByText('API offline')).toHaveAttribute('title', 'new failure'));

    resolveFirst({ running: true, address: '127.0.0.1:7575', error: '' });
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.getByText('API offline')).toHaveAttribute('title', 'new failure');
  });

  it('does not let a stale periodic fallback overwrite a newer scan event', async () => {
    vi.useFakeTimers();
    let resolveFallback!: (stations: StationInfo[]) => void;
    api.CheckAllStationStatuses.mockRejectedValue(new Error('temporary read failure'));
    api.GetCurrentStationInfo.mockReturnValue(new Promise((resolve) => {
      resolveFallback = resolve;
    }));
    render(App);
    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledOnce());
    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.CheckAllStationStatuses).toHaveBeenCalledOnce());

    runtime.handlers.get('external-scan-completed')?.([createStation({ name: 'LHB-NEW' })]);
    resolveFallback([createStation({ name: 'LHB-OLD' })]);
    await vi.advanceTimersByTimeAsync(0);

    expect(screen.getByText('LHB-NEW')).toBeInTheDocument();
    expect(screen.queryByText('LHB-OLD')).not.toBeInTheDocument();
    expect(screen.queryByText(/Status refresh incomplete/)).not.toBeInTheDocument();
  });
});
