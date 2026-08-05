import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from './lib/types';
import { pushToast } from './lib/toast';

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
  api.ScanAndFetchStations.mockResolvedValue([createStation()]);
  api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
  api.GetCurrentStationInfo.mockResolvedValue([createStation()]);
  api.CheckAllStationStatuses.mockResolvedValue([createStation()]);
  api.StopScan.mockResolvedValue(undefined);
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

  it('still starts the initial scan after a delayed startup scan check', async () => {
    vi.useFakeTimers();
    let resolveStartupScan!: (scanning: boolean) => void;
    api.IsScanning.mockReturnValueOnce(new Promise((resolve) => {
      resolveStartupScan = resolve;
    })).mockResolvedValue(false);
    render(App);

    await vi.advanceTimersByTimeAsync(15_000);
    expect(api.CheckAllStationStatuses).not.toHaveBeenCalled();
    resolveStartupScan(false);

    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledOnce());
    expect(await screen.findByText('LHB-TEST')).toBeInTheDocument();
  });

  it('does not overwrite an external scan started during the initial scan check', async () => {
    let resolveStartupScan!: (scanning: boolean) => void;
    api.IsScanning.mockReturnValueOnce(new Promise((resolve) => {
      resolveStartupScan = resolve;
    }));
    render(App);

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    resolveStartupScan(false);

    await vi.waitFor(() => expect(screen.getByText('Preparing external scan...')).toBeInTheDocument());
    expect(api.ScanAndFetchStations).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
  });

  it('shows a recoverable Bluetooth message when the initial scan fails', async () => {
    api.ScanAndFetchStations.mockRejectedValue(new Error('Bluetooth is unavailable; turn on Bluetooth and retry'));
    api.GetCurrentStationInfo.mockResolvedValue([
      createStation({ name: 'LHB-LATEST', connectionState: 'disconnected' })
    ]);
    render(App);
    expect((await screen.findAllByText(/Scan failed:.*Bluetooth is unavailable/)).length).toBeGreaterThan(0);
    expect(await screen.findByText('LHB-LATEST')).toBeInTheDocument();
    expect(api.GetCurrentStationInfo).toHaveBeenCalled();
  });

  it('refreshes station state after an external scan failure and keeps the scan error', async () => {
    render(App);
    await screen.findByText('LHB-TEST');
    api.GetCurrentStationInfo.mockResolvedValue([
      createStation({ name: 'LHB-AFTER-FAILURE', connectionState: 'disconnected' })
    ]);

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(1, { error: 'fixture radio failure' }));

    expect(await screen.findByText('LHB-AFTER-FAILURE')).toBeInTheDocument();
    expect(await screen.findByText(/External scan failed: fixture radio failure/)).toBeInTheDocument();
  });

  it('does not let an old external failure refresh overwrite a newer scan start', async () => {
    render(App);
    await screen.findByText('LHB-TEST');
    let resolveFailureRefresh!: (stations: StationInfo[]) => void;
    api.GetCurrentStationInfo.mockReturnValueOnce(new Promise((resolve) => {
      resolveFailureRefresh = resolve;
    }));

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(1, { error: 'old failure' }));
    runtime.handlers.get('external-scan-started')?.(externalScanEvent(2));
    resolveFailureRefresh([createStation({ name: 'LHB-STALE' })]);
    await Promise.resolve();
    await Promise.resolve();

    expect(screen.queryByText('LHB-STALE')).not.toBeInTheDocument();
    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();
  });

  it('does not let an older terminal event end a newer external scan', async () => {
    let resolveStatus!: (value: unknown) => void;
    api.GetScanStatus.mockReturnValueOnce(new Promise((resolve) => {
      resolveStatus = resolve;
    }));
    render(App);
    await screen.findByText('LHB-TEST');
    const initialStatusCalls = api.GetScanStatus.mock.calls.length;

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(1, {
      stations: [createStation({ name: 'LHB-STALE' })]
    }));
    await waitFor(() => expect(api.GetScanStatus).toHaveBeenCalledTimes(initialStatusCalls + 1));
    runtime.handlers.get('external-scan-started')?.(externalScanEvent(2));
    resolveStatus({ state: 'completed', found: 1, warnings: [] });
    await Promise.resolve();

    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
    expect(screen.queryByText('LHB-STALE')).not.toBeInTheDocument();
  });

  it('ignores a stale external start after a newer scan has terminated', async () => {
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(2));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(2, { stations: [createStation()] }));
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('External scan completed'));

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    await Promise.resolve();

    expect(screen.queryByText('Preparing external scan...')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('preserves a station update completed while external scan completion is pending', async () => {
    let resolveStatus!: (status: unknown) => void;
    const scannedStation = createStation({ powerState: 0, powerStateName: 'sleep' });
    const updatedStation = createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b });
    render(App);
    await screen.findByText('LHB-TEST');
    api.GetScanStatus.mockReturnValueOnce(new Promise((resolve) => {
      resolveStatus = resolve;
    }));
    api.SetStationPower.mockResolvedValue({ station: updatedStation, commandSent: true, confirmed: true });

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(1, { stations: [scannedStation] }));
    await waitFor(() => expect(api.GetScanStatus).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    await waitFor(() => expect(screen.getByText('On confirmed')).toBeInTheDocument());

    resolveStatus({ state: 'completed', found: 1, warnings: [] });
    await waitFor(() => expect(screen.getByText('On confirmed')).toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('ignores a terminal event for an unknown external scan until the backend scan ends', async () => {
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValue(false);
    render(App);
    await screen.findByRole('button', { name: 'Stop' });

    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(9, {
      stations: [createStation({ name: 'LHB-STALE-EXTERNAL' })]
    }));
    await Promise.resolve();

    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();
    expect(screen.queryByText('LHB-STALE-EXTERNAL')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
  });

  it('ignores a delayed external completion after a newer local scan starts', async () => {
    let resolveLocalScan!: (stations: StationInfo[]) => void;
    api.ScanAndFetchStations.mockReturnValueOnce(new Promise((resolve) => {
      resolveLocalScan = resolve;
    }));
    render(App);
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(1, {
      stations: [createStation({ name: 'LHB-STALE-EXTERNAL' })]
    }));
    resolveLocalScan([createStation({ name: 'LHB-LOCAL' })]);

    expect(await screen.findByText('LHB-LOCAL')).toBeInTheDocument();
    expect(screen.queryByText('LHB-STALE-EXTERNAL')).not.toBeInTheDocument();
  });

  it('preserves a station update started during an external failure refresh', async () => {
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValue(false);
    render(App);
    await screen.findByText('LHB-TEST');
    let resolveRefresh!: (stations: StationInfo[]) => void;
    api.GetCurrentStationInfo.mockReturnValueOnce(new Promise((resolve) => {
      resolveRefresh = resolve;
    }));
    api.SetStationPower.mockResolvedValue({
      station: createStation({ powerState: 1, powerStateName: 'on' }),
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(1, { error: 'fixture failure' }));
    await waitFor(() => expect(api.GetCurrentStationInfo).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    expect(await screen.findByText('On confirmed')).toBeInTheDocument();
    resolveRefresh([createStation({ powerState: 0, powerStateName: 'sleep' })]);

    await waitFor(() => expect(screen.getByText('On confirmed')).toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('does not run periodic status reads during an external scan', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(true);
    render(App);
    await vi.waitFor(() => expect(api.IsScanning).toHaveBeenCalled());
    await vi.advanceTimersByTimeAsync(30_000);
    expect(api.CheckAllStationStatuses).not.toHaveBeenCalled();
  });

  it('keeps card controls available while a periodic status refresh is pending', async () => {
    vi.useFakeTimers();
    let resolveStatusRefresh!: (stations: StationInfo[]) => void;
    api.CheckAllStationStatuses.mockReturnValue(new Promise((resolve) => {
      resolveStatusRefresh = resolve;
    }));
    render(App);
    await vi.waitFor(() => expect(screen.getByText('LHB-TEST')).toBeInTheDocument());
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toBeEnabled());

    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.CheckAllStationStatuses).toHaveBeenCalledOnce());

    expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toBeEnabled();
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    expect(await screen.findByRole('button', { name: 'Identify' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Refresh capabilities' })).toBeEnabled();

    resolveStatusRefresh([createStation()]);
  });

  it('allows only one foreground GATT action while status refresh uses the other slot', async () => {
    vi.useFakeTimers();
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB]);
    let resolveStatusRefresh!: (stations: StationInfo[]) => void;
    api.CheckAllStationStatuses.mockReturnValue(new Promise((resolve) => {
      resolveStatusRefresh = resolve;
    }));
    let resolvePower!: (result: unknown) => void;
    api.SetStationPower.mockReturnValue(new Promise((resolve) => {
      resolvePower = resolve;
    }));
    render(App);
    await vi.waitFor(() => expect(screen.getByText('LHB-B')).toBeInTheDocument());

    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.CheckAllStationStatuses).toHaveBeenCalledOnce());
    const first = screen.getByRole('button', { name: 'Turn LHB-A on' });
    const second = screen.getByRole('button', { name: 'Turn LHB-B on' });
    expect(first).toBeEnabled();
    expect(second).toBeEnabled();
    await fireEvent.click(first);
    await vi.waitFor(() => expect(api.SetStationPower).toHaveBeenCalledWith('AA', 'on'));
    expect(second).toBeDisabled();

    resolvePower({
      station: { ...stationA, powerState: 1, powerStateName: 'on', rawPowerState: 0x0b },
      commandSent: true, confirmed: true, confirmationError: ''
    });
    resolveStatusRefresh([stationA, stationB]);
  });

  it('self-recovers when polling observes an external scan has ended without an event', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-RECOVERED' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: ['partial metadata'] });
    render(App);
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled());

    await vi.advanceTimersByTimeAsync(15_000);

    expect(await screen.findByText('LHB-RECOVERED')).toBeInTheDocument();
    expect(screen.getByText('External scan completed: found 1; 1 known station(s). partial metadata')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('self-recovers a cancelled external scan without reporting completion', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(false);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled());

    await vi.advanceTimersByTimeAsync(15_000);

    expect(await screen.findByText('External scan stopped.')).toBeInTheDocument();
    expect(screen.queryByText(/External scan completed/)).not.toBeInTheDocument();
  });

  it('retries external terminal recovery when the first scan-status request fails', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-RECOVERED' })]);
    render(App);
    await screen.findByText('LHB-TEST');
    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    api.GetScanStatus.mockReset();
    api.GetScanStatus.mockRejectedValueOnce(new Error('temporary scan status failure'))
      .mockResolvedValue({ state: 'failed', found: 0, error: 'radio failure', warnings: ['partial cleanup'] });

    await vi.advanceTimersByTimeAsync(15_000);
    expect(screen.queryByText(/External scan failed/)).not.toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(15_000);
    expect(await screen.findByText('External scan failed: radio failure partial cleanup')).toBeInTheDocument();
  });

  it('retries failed external scan recovery after the first authoritative list request fails', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    render(App);
    await screen.findByText('LHB-TEST');
    api.GetCurrentStationInfo.mockRejectedValueOnce(new Error('temporary list failure'))
      .mockResolvedValue([createStation({ name: 'LHB-RECOVERED-FAILURE' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'failed', found: 0, error: 'radio failure', warnings: ['partial cleanup'] });

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(1, { error: 'radio failure' }));
    await vi.advanceTimersByTimeAsync(15_000);

    expect(await screen.findByText('LHB-RECOVERED-FAILURE')).toBeInTheDocument();
    expect(screen.getByText('External scan failed: radio failure partial cleanup')).toBeInTheDocument();
  });

  it('retries cancelled external scan recovery after the first scan-status request fails', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-RECOVERED-CANCEL' })]);
    render(App);
    await screen.findByText('LHB-TEST');
    api.GetScanStatus.mockRejectedValueOnce(new Error('temporary scan status failure'))
      .mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-cancelled')?.(externalScanEvent(1));
    await vi.advanceTimersByTimeAsync(15_000);
	await vi.advanceTimersByTimeAsync(15_000);

    expect(await screen.findByText('LHB-RECOVERED-CANCEL')).toBeInTheDocument();
    expect(screen.getByText('External scan stopped.')).toBeInTheDocument();
  });

  it('applies a delayed cancellation event after StopScan has already settled', async () => {
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValue(false);
    api.StopScan.mockResolvedValue(undefined);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-AFTER-STOP' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: ['partial cleanup'] });
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(7));
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();

    runtime.handlers.get('external-scan-cancelled')?.(externalScanEvent(7));

    expect(await screen.findByText('LHB-AFTER-STOP')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('recovers immediately when an external stop has no terminal event', async () => {
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValue(false);
    api.StopScan.mockResolvedValue(undefined);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-STOP-RECOVERED' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(7));
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));

    expect(await screen.findByText('LHB-STOP-RECOVERED')).toBeInTheDocument();
    expect(await screen.findByText('External scan stopped.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('claims only one terminal event for an adopted external scan', async () => {
    const terminalChecks: Array<(scanning: boolean) => void> = [];
    api.IsScanning.mockResolvedValueOnce(true).mockImplementation(() => new Promise((resolve) => {
      terminalChecks.push(resolve);
    }));
    render(App);
    await screen.findByRole('button', { name: 'Stop' });

    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(9, { stations: [createStation({ name: 'LHB-COMPLETED' })] }));
    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(9, { error: 'stale failure' }));
    await waitFor(() => expect(terminalChecks).toHaveLength(2));
    terminalChecks[0](false);
    terminalChecks[1](false);

    expect(await screen.findByText('LHB-COMPLETED')).toBeInTheDocument();
    expect(screen.queryByText(/External scan failed/)).not.toBeInTheDocument();
    expect(pushToast).not.toHaveBeenCalledWith('External scan failed: stale failure');
  });

  it('ignores an older terminal event while recovering an adopted external scan', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValueOnce(true).mockResolvedValue(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-RECOVERED-NEW' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(7));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(7, { stations: [createStation()] }));
    await screen.findByText('External scan completed: found 1; 1 known station(s).');
    await vi.advanceTimersByTimeAsync(15_000);
    await screen.findByRole('button', { name: 'Stop' });

    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(7, { error: 'stale failure' }));
    await Promise.resolve();
    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(15_000);
    expect(await screen.findByText('LHB-RECOVERED-NEW')).toBeInTheDocument();
    expect(screen.queryByText(/stale failure/)).not.toBeInTheDocument();
  });

  it('retries external stop recovery when the first authoritative list fails', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    api.StopScan.mockResolvedValue(undefined);
    api.GetCurrentStationInfo.mockRejectedValueOnce(new Error('temporary list failure'))
      .mockResolvedValue([createStation({ name: 'LHB-STOP-LIST-RECOVERED' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(7));
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    expect(screen.queryByText('External scan stopped.')).not.toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(15_000);
    expect(await screen.findByText('LHB-STOP-LIST-RECOVERED')).toBeInTheDocument();
    expect(screen.getByText('External scan stopped.')).toBeInTheDocument();
  });

  it('retries external stop recovery when the first scan-status request fails', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    api.StopScan.mockResolvedValue(undefined);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-STOP-STATUS-RECOVERED' })]);
    render(App);
    await screen.findByText('LHB-TEST');
    api.GetScanStatus.mockRejectedValueOnce(new Error('temporary scan status failure'))
      .mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(7));
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    expect(screen.queryByText('External scan stopped.')).not.toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(15_000);
    expect(await screen.findByText('LHB-STOP-STATUS-RECOVERED')).toBeInTheDocument();
    expect(screen.getByText('External scan stopped.')).toBeInTheDocument();
  });

  it('does not let stale external recovery overwrite a later local scan result', async () => {
    vi.useFakeTimers();
    let resolveExternalList!: (stations: StationInfo[]) => void;
    api.IsScanning.mockResolvedValue(false);
    render(App);
    await screen.findByText('LHB-TEST');
    api.GetCurrentStationInfo.mockReturnValueOnce(new Promise((resolve) => {
      resolveExternalList = resolve;
    }));
    api.ScanAndFetchStations.mockResolvedValueOnce([createStation({ name: 'LHB-LOCAL-FINAL' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(8));
    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(8, { error: 'old failure' }));
    await waitFor(() => expect(api.GetCurrentStationInfo).toHaveBeenCalledOnce());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Scan' }));
    expect(await screen.findByText('LHB-LOCAL-FINAL')).toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Found 1; 1 known station(s).');

    resolveExternalList([createStation({ name: 'LHB-STALE-EXTERNAL' })]);
    await vi.advanceTimersByTimeAsync(15_000);

    expect(screen.queryByText('LHB-STALE-EXTERNAL')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Found 1; 1 known station(s).');
    expect(screen.queryByText(/External scan completed/)).not.toBeInTheDocument();
  });

  it('keeps terminal recovery pending when an external completion status request fails', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    render(App);
    await screen.findByText('LHB-TEST');
    api.GetScanStatus.mockReset();
    api.GetScanStatus.mockRejectedValueOnce(new Error('temporary scan status failure'))
      .mockResolvedValue({ state: 'failed', found: 0, error: 'radio failure', warnings: [] });

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(1, {
      stations: [createStation({ name: 'LHB-COMPLETED' })]
    }));
    expect(await screen.findByText('LHB-COMPLETED')).toBeInTheDocument();
    await Promise.resolve();

    await vi.advanceTimersByTimeAsync(15_000);
    expect(await screen.findByText('External scan failed: radio failure')).toBeInTheDocument();
  });

  it('stops an active scan and handles its cancellation event', async () => {
    let resolveStop!: () => void;
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValue(false);
    api.StopScan.mockReturnValue(new Promise<void>((resolve) => {
      resolveStop = resolve;
    }));
    render(App);
    const stop = await screen.findByRole('button', { name: 'Stop' });
    await fireEvent.click(stop);
    expect(api.StopScan).toHaveBeenCalledOnce();
    expect(await screen.findByRole('button', { name: 'Stopping...' })).toBeDisabled();

    runtime.handlers.get('external-scan-cancelled')?.(externalScanEvent(1));
    expect(await screen.findByText('Scan stopped.')).toBeInTheDocument();
    resolveStop();
  });

  it('does not let the stop promise overwrite a cancellation event', async () => {
    let resolveStop!: () => void;
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValue(false);
    api.StopScan.mockReturnValue(new Promise<void>((resolve) => {
      resolveStop = resolve;
    }));
    render(App);
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));

    runtime.handlers.get('external-scan-cancelled')?.(externalScanEvent(1));
    expect(await screen.findByText('Scan stopped.')).toBeInTheDocument();
    resolveStop();
    await Promise.resolve();

    expect(screen.getByText('Scan stopped.')).toBeInTheDocument();
    expect(screen.queryByText('Scan stop requested...')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('does not let a late stop rejection overwrite a cancellation event', async () => {
    let rejectStop!: (error: Error) => void;
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValue(false);
    api.StopScan.mockReturnValue(new Promise<void>((_, reject) => {
      rejectStop = reject;
    }));
    render(App);
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));

    runtime.handlers.get('external-scan-cancelled')?.(externalScanEvent(1));
    expect(await screen.findByText('Scan stopped.')).toBeInTheDocument();
    rejectStop(new Error('late stop failure'));
    await Promise.resolve();

    expect(screen.getByText('Scan stopped.')).toBeInTheDocument();
    expect(screen.queryByText(/Unable to stop scan/)).not.toBeInTheDocument();
  });

  it('keeps a local scan stopping until the StopScan promise settles', async () => {
    let rejectScan!: (error: Error) => void;
    let resolveStop!: () => void;
    api.ScanAndFetchStations.mockReturnValue(new Promise((_, reject) => { rejectScan = reject; }));
    api.StopScan.mockReturnValue(new Promise<void>((resolve) => { resolveStop = resolve; }));
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    rejectScan(new Error('scan cancelled'));

    expect(await screen.findByRole('button', { name: 'Stopping...' })).toBeDisabled();
    resolveStop();
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('keeps a local scan stopping when StopScan settles before the scan promise', async () => {
    let rejectScan!: (error: Error) => void;
    api.ScanAndFetchStations.mockReturnValue(new Promise((_, reject) => { rejectScan = reject; }));
    api.StopScan.mockResolvedValue(undefined);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);

    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    expect(await screen.findByRole('button', { name: 'Stopping...' })).toBeDisabled();

    rejectScan(new Error('scan cancelled'));
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('keeps a scan started while a previous StopScan is settling stoppable', async () => {
    let rejectFirstScan!: (error: Error) => void;
    let resolveSecondScan!: (stations: StationInfo[]) => void;
    let resolveStop!: () => void;
    api.ScanAndFetchStations
      .mockReturnValueOnce(new Promise((_, reject) => { rejectFirstScan = reject; }))
      .mockReturnValueOnce(new Promise((resolve) => { resolveSecondScan = resolve; }));
    api.GetCurrentStationInfo.mockResolvedValue([]);
    api.StopScan.mockReturnValue(new Promise<void>((resolve) => { resolveStop = resolve; }));
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);

    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    rejectFirstScan(new Error('scan cancelled'));
    expect(await screen.findByRole('button', { name: 'Stopping...' })).toBeDisabled();

    // The empty fleet offers Scan Now while the old StopScan promise is still
    // pending. The pending stop belongs to the superseded scan, so the new
    // scan must start in its own running state with a working Stop control.
    await fireEvent.click(await screen.findByRole('button', { name: 'Scan Now' }));
    expect(api.ScanAndFetchStations).toHaveBeenCalledTimes(2);

    const stop = await screen.findByRole('button', { name: 'Stop' });
    expect(stop).toBeEnabled();
    await fireEvent.click(stop);
    expect(api.StopScan).toHaveBeenCalledTimes(2);

    resolveStop();
    resolveSecondScan([createStation({ name: 'LHB-AFTER-RESUME' })]);
    expect(await screen.findByText('LHB-AFTER-RESUME')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('keeps the stopping message while polling a pending external stop', async () => {
    vi.useFakeTimers();
    let resolveStop!: () => void;
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValue(true);
    api.StopScan.mockReturnValue(new Promise<void>((resolve) => { resolveStop = resolve; }));
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    api.GetCurrentStationInfo.mockResolvedValue([createStation()]);
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(9));
    await screen.findByText('Preparing external scan...');
    await fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(screen.getByRole('status')).toHaveTextContent('Stopping scan...');

    await vi.advanceTimersByTimeAsync(15_000);
    expect(screen.getByRole('status')).toHaveTextContent('Stopping scan...');

    api.IsScanning.mockResolvedValue(false);
    resolveStop();
    await vi.advanceTimersByTimeAsync(0);
  });

  it('treats a local scan cancellation as stopped instead of failed', async () => {
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    api.ScanAndFetchStations.mockRejectedValue(new Error('scan cancelled'));
    render(App);

    expect(await screen.findByText('Scan stopped.')).toBeInTheDocument();
    expect(pushToast).not.toHaveBeenCalledWith(expect.stringContaining('Scan failed'));
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
    expect(screen.queryByText('On confirmed')).not.toBeInTheDocument();
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

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));

    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Station name' })).not.toBeInTheDocument());
    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();
  });

  it('keeps a rename draft open while periodic status refresh continues', async () => {
    vi.useFakeTimers();
    render(App);
    await vi.waitFor(() => expect(screen.getByText('LHB-TEST')).toBeInTheDocument());
    await fireEvent.click(screen.getByRole('button', { name: 'Rename LHB-TEST' }));
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Draft name' } });

    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.CheckAllStationStatuses).toHaveBeenCalledOnce());
    expect(screen.getByDisplayValue('Draft name')).toBeInTheDocument();
  });

  it('saves and cancels rename through standard keyboard button activation without blur submission', async () => {
    api.RenameStationByAddress.mockResolvedValue(undefined);
    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Rename LHB-TEST' }));
    await fireEvent.input(screen.getByRole('textbox', { name: 'Station name' }), { target: { value: 'Keyboard name' } });
    await fireEvent.keyDown(screen.getByTitle('Save name'), { key: 'Enter' });
    await fireEvent.click(screen.getByTitle('Save name'));
    await waitFor(() => expect(api.RenameStationByAddress).toHaveBeenCalledOnce());

    await fireEvent.click(await screen.findByRole('button', { name: 'Rename Keyboard name' }));
    await fireEvent.input(screen.getByRole('textbox', { name: 'Station name' }), { target: { value: 'Discard me' } });
    await fireEvent.keyDown(screen.getByTitle('Cancel'), { key: ' ' });
    await fireEvent.click(screen.getByTitle('Cancel'));
    expect(api.RenameStationByAddress).toHaveBeenCalledOnce();
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

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Change channel' })).not.toBeInTheDocument());
    expect(screen.queryByText('On confirmed')).not.toBeInTheDocument();
    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();
  });

  it('keeps the channel result visible by preventing close while a save is pending', async () => {
    let resolveChannel!: (value: unknown) => void;
    api.SetStationChannel.mockReturnValue(new Promise((resolve) => {
      resolveChannel = resolve;
    }));

    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await fireEvent.click(await screen.findByRole('button', { name: /Change Channel/ }));
    await fireEvent.click(within(screen.getByRole('dialog', { name: 'Change channel' })).getByRole('button', { name: '4' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm change' }));
    await waitFor(() => expect(api.SetStationChannel).toHaveBeenCalledWith('11:22:33:44:55:66', 4, false));

    const channelDialog = screen.getByRole('dialog', { name: 'Change channel' });
    await waitFor(() => expect(within(channelDialog).getByRole('button', { name: 'Close' })).toBeDisabled());
    await fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: 'Change channel' })).toBeInTheDocument();

    resolveChannel({ previousChannel: 3, channel: 4, warnings: [] });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Change channel' })).not.toBeInTheDocument());
  });

  it('keeps unrelated backend errors after a confirmed channel change', async () => {
    const initial = createStation({ lastError: 'metadata: firmware read failed' });
    const updated = createStation({
      channel: 4,
      channelFresh: true,
      lastError: 'metadata: firmware read failed'
    });
    api.ScanAndFetchStations.mockResolvedValue([initial]);
    api.GetCurrentStationInfo.mockResolvedValue([updated]);
    api.SetStationChannel.mockResolvedValue({
      address: updated.address,
      previousChannel: 3,
      channel: 4,
      commandSent: true,
      confirmed: true,
      confirmationError: '',
      warnings: [],
      station: updated
    });

    render(App);
    await screen.findByText('LHB-TEST');
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await fireEvent.click(await screen.findByRole('button', { name: /Change Channel/ }));
    await fireEvent.click(within(screen.getByRole('dialog', { name: 'Change channel' })).getByRole('button', { name: '4' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm change' }));

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Change channel' })).not.toBeInTheDocument());
    expect(screen.getByText('metadata: firmware read failed')).toBeInTheDocument();
  });

  it('disables channel changes while a station is freshly booting', async () => {
    api.ScanAndFetchStations.mockResolvedValue([
      createStation({ powerState: 3, powerStateName: 'booting', powerFresh: true })
    ]);

    render(App);
    await screen.findByText('LHB-TEST');
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));

    expect(await screen.findByRole('button', { name: /Change Channel/ })).toBeDisabled();
    expect(screen.getByText(/Wait for the station to finish booting before changing its channel/)).toBeInTheDocument();
  });

  it('allows submitting the same last-known channel from App and keeps an unconfirmed result open', async () => {
    api.ScanAndFetchStations.mockResolvedValue([createStation({ channelFresh: false })]);
    api.SetStationChannel.mockResolvedValue({
      previousChannel: 3,
      channel: 3,
      warnings: [],
      commandSent: true,
      confirmed: false,
      confirmationError: 'readback timed out'
    });
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ channelFresh: false })]);
    render(App);
    await screen.findByText('LHB-TEST');
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await fireEvent.click(await screen.findByRole('button', { name: /Change Channel/ }));
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm change' }));

    await waitFor(() => expect(api.SetStationChannel).toHaveBeenCalledWith('11:22:33:44:55:66', 3, false));
    expect(screen.getByRole('dialog', { name: 'Change channel' })).toBeInTheDocument();
    const warning = await screen.findByText(/Channel command sent but unconfirmed: readback timed out/);
    expect(warning).toHaveClass('warning');
    expect(warning).toHaveTextContent('Readback: last-known 3');
  });

  it('uses the authoritative channel result without another station-list read', async () => {
    const updated = createStation({
      channel: 4,
      channelFresh: true,
      lastChannelReadAt: '2026-07-29T08:00:00Z'
    });
    api.SetStationChannel.mockResolvedValue({
      address: updated.address,
      previousChannel: 3,
      channel: 4,
      warnings: [],
      commandSent: true,
      confirmed: false,
      confirmationError: 'readback timed out',
      station: updated
    });
    api.GetCurrentStationInfo.mockRejectedValue(new Error('fixture list failure'));

    render(App);
    await screen.findByText('LHB-TEST');
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await fireEvent.click(await screen.findByRole('button', { name: /Change Channel/ }));
    await fireEvent.click(within(screen.getByRole('dialog', { name: 'Change channel' })).getByRole('button', { name: '4' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm change' }));

    const warning = await screen.findByText(/Channel command sent but unconfirmed: readback timed out/);
    expect(warning).toHaveTextContent('Readback: actual 4');
    expect(api.GetCurrentStationInfo).not.toHaveBeenCalled();
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

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled());
    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();

    resolvePower({
      station: createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }),
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    await Promise.resolve();

    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();
    expect(screen.queryByText('On confirmed')).not.toBeInTheDocument();
  });

  it('releases device busy state when an operation settles during an external scan', async () => {
    let resolvePower!: (value: unknown) => void;
    api.SetStationPower.mockReturnValue(new Promise((resolve) => {
      resolvePower = resolve;
    }));

    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    const onButton = screen.getByRole('button', { name: 'Turn LHB-TEST on' });
    await fireEvent.click(onButton);
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledOnce());
    expect(onButton).toHaveClass('pending');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled());

    resolvePower({
      station: createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }),
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    await waitFor(() => expect(onButton).not.toHaveClass('pending'));
    // The result commit stays epoch-gated: no stale success note mid-scan.
    expect(screen.queryByText('On confirmed')).not.toBeInTheDocument();
  });

  it('expires a stale pending power feedback that never settles', async () => {
    vi.useFakeTimers();
    api.SetStationPower.mockReturnValue(new Promise(() => {}));

    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledOnce());
    expect(await screen.findByText('Switching to On…')).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(60_000);
    expect(screen.queryByText('Switching to On…')).not.toBeInTheDocument();
  });

  it('keeps the channel display stable across transient channel wipes', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    render(App);
    await screen.findByText('LHB-TEST');

    // Initial scan: channel 3 is occupied and the card chip shows it.
    expect(screen.getByRole('button', { name: 'CH 3 — LHB-TEST · sleep' })).toBeEnabled();
    expect(screen.getByText('CH 03')).toBeInTheDocument();

    // A later background poll reports the channel wiped (transient capability
    // loss), alongside a second station whose channel is genuinely unknown.
    const wipedA = createStation({ channel: 0, channelFresh: false, statusFresh: false });
    const unknownB = createStation({ name: 'LHB-B', address: 'BB', channel: 0, channelFresh: false });
    api.CheckAllStationStatuses.mockResolvedValue([wipedA, unknownB]);
    api.GetCurrentStationInfo.mockResolvedValue([wipedA, unknownB]);
    await vi.advanceTimersByTimeAsync(15_000);

    // Display hysteresis: the cell stays occupied as last-known and the card
    // chip keeps CH 03 instead of flipping to free/CH --.
    const staleCell = screen.getByRole('button', { name: 'CH 3 — LHB-TEST · sleep · last-known' });
    expect(staleCell).toBeEnabled();
    expect(staleCell).toHaveClass('stale');
    expect(screen.getByText('CH 03')).toBeInTheDocument();

    // Safety logic is untouched: the channel modal still uses the live data
    // and demands explicit risk confirmation for the genuinely unknown station.
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await fireEvent.click(await screen.findByRole('button', { name: /Change Channel/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Change channel' });
    expect(within(dialog).getByText(/unknown channel/)).toBeInTheDocument();
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    // Once the memory window expires with the channel still unknown, the
    // display falls back to the live value.
    await vi.advanceTimersByTimeAsync(60_000);
    expect(screen.getByRole('button', { name: 'CH 3 — free' })).toBeDisabled();
    expect(screen.queryByText('CH 03')).not.toBeInTheDocument();
  });

  it('clears selection and channel editor state when a status refresh drops the selected station', async () => {
    vi.useFakeTimers();
    // flip queries running animations when a card leaves the grid; jsdom has
    // none, and without a stub the removed card's ghost never detaches.
    Object.defineProperty(Element.prototype, 'getAnimations', {
      configurable: true,
      value: vi.fn(() => [])
    });
    api.IsScanning.mockResolvedValue(false);
    render(App);
    await screen.findByText('LHB-TEST');
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await fireEvent.click(await screen.findByRole('button', { name: /Change Channel/ }));
    expect(screen.getByRole('dialog', { name: 'Change channel' })).toBeInTheDocument();

    // A periodic list replacement that no longer contains the station must
    // drop the selection together with the stale channel-editor state.
    const replacement = createStation({ name: 'LHB-OTHER', address: 'AA:BB:CC:DD:EE:FF' });
    api.CheckAllStationStatuses.mockResolvedValue([replacement]);
    api.GetCurrentStationInfo.mockResolvedValue([replacement]);
    await vi.advanceTimersByTimeAsync(15_000);

    await vi.waitFor(() => expect(screen.queryByRole('dialog', { name: 'Change channel' })).not.toBeInTheDocument());
    await vi.waitFor(() => expect(screen.queryByRole('dialog', { name: 'Station details' })).not.toBeInTheDocument());
    expect(screen.queryByText('LHB-TEST')).not.toBeInTheDocument();

    // When the station reappears, the drawer must not silently reopen,
    // and a fresh details action must show an active drawer without the
    // channel modal that belonged to the stale selection.
    api.CheckAllStationStatuses.mockResolvedValue([replacement, createStation()]);
    api.GetCurrentStationInfo.mockResolvedValue([replacement, createStation()]);
    await vi.advanceTimersByTimeAsync(15_000);
    await screen.findByText('LHB-TEST');
    expect(screen.queryByRole('dialog', { name: 'Station details' })).not.toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    const drawer = await screen.findByRole('dialog', { name: 'Station details' });
    expect(drawer).toHaveAttribute('aria-modal', 'true');
    expect(screen.queryByRole('dialog', { name: 'Change channel' })).not.toBeInTheDocument();
  });

  it('clears stale device busy state when an external scan supersedes a settled backend operation', async () => {
    let resolvePower!: (value: unknown) => void;
    api.SetStationPower.mockReturnValue(new Promise((resolve) => {
      resolvePower = resolve;
    }));
    const scannedStation = createStation({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0 });

    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Turn LHB-TEST on' }));
    await waitFor(() => expect(api.SetStationPower).toHaveBeenCalledOnce());

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(1, { stations: [scannedStation] }));

    await waitFor(() => expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).not.toBeDisabled());
    resolvePower({
      station: createStation({ powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }),
      commandSent: true,
      confirmed: true,
      confirmationError: ''
    });
    await Promise.resolve();
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

  it('disables a bulk target when every station is already there or booting', async () => {
    const stationOn = createStation({
      name: 'LHB-ON', address: 'AA', powerState: 1, powerStateName: 'on', rawPowerState: 0x0b
    });
    const stationBooting = createStation({
      name: 'LHB-BOOTING', address: 'BB', powerState: 3, powerStateName: 'booting',
      powerStateConfirmed: false, rawPowerState: 0x01
    });
    api.ScanAndFetchStations.mockResolvedValue([stationOn, stationBooting]);
    api.GetCurrentStationInfo.mockResolvedValue([stationOn, stationBooting]);

    render(App);
    await screen.findByText('LHB-BOOTING');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());
    const bulkControls = screen.getByLabelText('Set all known stations');
    const bulkOn = within(bulkControls).getByRole('button', { name: 'On' });
    expect(bulkOn).toBeDisabled();
    expect(bulkOn).not.toHaveClass('active');
    await fireEvent.click(bulkOn);
    expect(api.SetAllStationsPowerDetailed).not.toHaveBeenCalled();
  });

  it('keeps a bulk target enabled when at least one station is actionable', async () => {
    const stationOn = createStation({
      name: 'LHB-ON', address: 'AA', powerState: 1, powerStateName: 'on', rawPowerState: 0x0b
    });
    const stationSleep = createStation({ name: 'LHB-SLEEP', address: 'BB' });
    api.ScanAndFetchStations.mockResolvedValue([stationOn, stationSleep]);
    api.GetCurrentStationInfo.mockResolvedValue([stationOn, stationSleep]);

    render(App);
    await screen.findByText('LHB-SLEEP');
    const bulkOn = within(screen.getByLabelText('Set all known stations'))
      .getByRole('button', { name: 'On' });
    await waitFor(() => expect(bulkOn).toBeEnabled());
  });

  it('uses success severity when a bulk request becomes a pure no-op', async () => {
    const station = createStation();
    api.SetAllStationsPowerDetailed.mockResolvedValue({
      target: 'on',
      results: [{
        address: station.address, name: station.name, skipped: true,
        reason: 'already at target state', commandSent: false, success: true,
        confirmed: true, error: '', station
      }]
    });

    render(App);
    await screen.findByText('LHB-TEST');
    const bulkOn = await screen.findByTitle('Turn all known stations on');
    await waitFor(() => expect(bulkOn).toBeEnabled());
    await fireEvent.click(bulkOn);

    await waitFor(() => expect(pushToast).toHaveBeenCalledWith(
      'Bulk On: 0 confirmed; 0 sent but unconfirmed; 1 already at target; 0 skipped for On.', 'success'
    ));
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

  it('reports complete bulk counts with warning severity for incomplete outcomes', async () => {
    const stationA = createStation({ name: 'LHB-A', address: 'AA' });
    const stationB = createStation({ name: 'LHB-B', address: 'BB' });
    const stationC = createStation({ name: 'LHB-C', address: 'CC' });
    api.ScanAndFetchStations.mockResolvedValue([stationA, stationB, stationC]);
    api.GetCurrentStationInfo.mockResolvedValue([stationA, stationB, stationC]);
    api.SetAllStationsPowerDetailed.mockResolvedValue({
      target: 'on',
      results: [
        { address: 'AA', name: 'LHB-A', skipped: false, reason: '', commandSent: true, success: true, confirmed: true, error: '', station: stationA },
        { address: 'BB', name: 'LHB-B', skipped: false, reason: '', commandSent: true, success: true, confirmed: false, error: 'readback timed out', station: stationB },
        { address: 'CC', name: 'LHB-C', skipped: true, reason: 'already at target', commandSent: false, success: true, confirmed: true, error: '', station: stationC }
      ]
    });
    render(App);
    await screen.findByText('LHB-C');
    await fireEvent.click(await screen.findByTitle('Turn all known stations on'));

    await waitFor(() => expect(pushToast).toHaveBeenCalledWith(
      'Bulk On: 1 confirmed; 1 sent but unconfirmed; 1 already at target; 0 skipped for On.', 'warning'
    ));
  });

  it('excludes unverified power states from fleet counts', async () => {
    api.ScanAndFetchStations.mockResolvedValue([
      createStation({ name: 'VERIFIED', address: 'AA', powerState: 1, powerStateName: 'on' }),
      createStation({ name: 'UNVERIFIED', address: 'BB', powerState: 1, powerStateName: 'on', powerStateConfirmed: false })
    ]);
    render(App);
    await screen.findByText('UNVERIFIED');
    expect(screen.getByText('1 On')).toBeInTheDocument();
    expect(screen.queryByText('2 On')).not.toBeInTheDocument();
  });

  it('locks the empty-state scan button while an external scan is running', async () => {
    api.ScanAndFetchStations.mockResolvedValue([]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 0, warnings: [] });

    render(App);
    expect(await screen.findByRole('button', { name: 'Scan Now' })).not.toBeDisabled();
    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));

    const scanningButtons = await screen.findAllByRole('button', { name: 'Stop' });
    expect(scanningButtons.length).toBeGreaterThan(0);
    for (const button of scanningButtons) expect(button).toBeEnabled();
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

  it('surfaces persistent configuration startup warnings', async () => {
    const warning = 'Configuration could not be loaded: invalid JSON';
    api.GetAPIStatus.mockResolvedValue({
      running: true,
      address: '127.0.0.1:7575',
      error: '',
      warnings: [warning],
      configWritable: false
    });

    render(App);

    expect(await screen.findByText('Config read-only')).toHaveAttribute('title', warning);
    expect(pushToast).toHaveBeenCalledWith(warning, 'warning');
  });

  it('reports the same configuration warning again after it clears and recurs', async () => {
    vi.useFakeTimers();
    const warning = 'Configuration changes could not be saved: disk full';
    const failed = {
      running: true, address: '127.0.0.1:7575', error: '', warnings: [warning], configWritable: false
    };
    const healthy = {
      running: true, address: '127.0.0.1:7575', error: '', warnings: [], configWritable: true
    };
    api.GetAPIStatus
      .mockResolvedValueOnce(failed)
      .mockResolvedValueOnce(failed)
      .mockResolvedValueOnce(healthy)
      .mockResolvedValueOnce(failed);

    render(App);
    await vi.waitFor(() => expect(api.GetAPIStatus).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(pushToast).toHaveBeenCalledWith(warning, 'warning'));

    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.GetAPIStatus).toHaveBeenCalledTimes(2));
    expect(vi.mocked(pushToast).mock.calls.filter(([message]) => message === warning)).toHaveLength(1);

    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.GetAPIStatus).toHaveBeenCalledTimes(3));
    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.GetAPIStatus).toHaveBeenCalledTimes(4));
    expect(vi.mocked(pushToast).mock.calls.filter(([message]) => message === warning)).toHaveLength(2);
  });

  it('refreshes configuration health immediately after a rename failure', async () => {
    const warning = 'Configuration changes could not be saved: disk full';
    api.RenameStationByAddress.mockRejectedValue(new Error('disk full'));
    api.GetAPIStatus
      .mockResolvedValueOnce({
        running: true, address: '127.0.0.1:7575', error: '', warnings: [], configWritable: true
      })
      .mockResolvedValueOnce({
        running: true, address: '127.0.0.1:7575', error: '', warnings: [warning], configWritable: false
      });

    render(App);
    await screen.findByText('LHB-TEST');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled());
    await fireEvent.click(screen.getByRole('button', { name: 'Rename LHB-TEST' }));
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'New name' } });
    await fireEvent.click(screen.getByTitle('Save name'));

    expect(await screen.findByText('Config read-only')).toHaveAttribute('title', warning);
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

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(1));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(1, {
      stations: [createStation({ name: 'LHB-NEW' })]
    }));
    resolveFallback([createStation({ name: 'LHB-OLD' })]);
    await vi.advanceTimersByTimeAsync(0);

    expect(screen.getByText('LHB-NEW')).toBeInTheDocument();
    expect(screen.queryByText('LHB-OLD')).not.toBeInTheDocument();
    expect(screen.queryByText(/Status refresh incomplete/)).not.toBeInTheDocument();
  });
});
