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

  it('does not adopt an auto-sleep scan as an external scan', async () => {
    vi.useFakeTimers();
    let resolveStartupScan!: (scanning: boolean) => void;
    api.IsScanning.mockImplementation(() => new Promise((resolve) => {
      resolveStartupScan = resolve;
    }));
    render(App);

    // The auto-sleep trigger starts between the poll's IsScanning request and
    // its continuation; its internal scan must not be adopted as external.
    runtime.handlers.get('auto-sleep')?.({ phase: 'started' });
    resolveStartupScan(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.queryByText('Preparing external scan...')).not.toBeInTheDocument();
    expect(screen.queryByText('External scan in progress...')).not.toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(15_000);
    expect(screen.queryByText('Preparing external scan...')).not.toBeInTheDocument();
    expect(screen.queryByText('External scan in progress...')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
    expect(screen.getByText('Auto sleep: scanning and putting all stations to sleep...')).toBeInTheDocument();
  });

  it('keeps the status line when the terminal scan status read hits a new scan', async () => {
    vi.useFakeTimers();
    api.GetScanStatus.mockResolvedValue({ state: 'running', found: 0, warnings: [] });
    render(App);
    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledOnce());
    await vi.waitFor(() => expect(api.GetScanStatus).toHaveBeenCalled());
    expect(await screen.findByText('LHB-TEST')).toBeInTheDocument();
    expect(screen.queryByText(/no stations found/i)).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Scanning for base stations...');
  });

  it('treats a failed scan superseded by a running scan as stopped', async () => {
    api.ScanAndFetchStations.mockRejectedValue(new Error('bluetooth scan cancelled'));
    api.GetScanStatus.mockResolvedValue({ state: 'running', found: 0, warnings: [] });
    render(App);
    expect(await screen.findByText('Scan stopped.')).toBeInTheDocument();
    expect(screen.queryByText(/Scan failed/)).not.toBeInTheDocument();
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
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockResolvedValue(false);
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

  it('recovers the finished external scan instead of adopting it at startup', async () => {
    let resolveStartupScan!: (scanning: boolean) => void;
    api.IsScanning.mockReturnValueOnce(new Promise((resolve) => {
      resolveStartupScan = resolve;
    }));
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-AFTER-RACE' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
    render(App);

    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(3, {
      stations: [createStation()]
    }));
    await Promise.resolve();
    resolveStartupScan(true);

    expect(await screen.findByText('LHB-AFTER-RACE')).toBeInTheDocument();
    expect(screen.queryByText('Preparing external scan...')).not.toBeInTheDocument();
    expect(screen.queryByText('External scan in progress...')).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('External scan completed'));
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('recovers instead of adopting when an external scan ends before a poll adopts it', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-POLL-RECOVERED' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(5, { stations: [createStation()] }));
    api.IsScanning.mockResolvedValueOnce(true);
    await vi.advanceTimersByTimeAsync(15_000);

    expect(await screen.findByText('LHB-POLL-RECOVERED')).toBeInTheDocument();
    expect(screen.queryByText('Preparing external scan...')).not.toBeInTheDocument();
    expect(screen.queryByText('External scan in progress...')).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('External scan stopped.'));
  });

  it('applies a remembered untracked terminal during the next status poll', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-PENDING-TERMINAL' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'failed', found: 0, error: 'radio failure', warnings: [] });
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-failed')?.(externalScanEvent(6, { error: 'radio failure' }));
    await vi.advanceTimersByTimeAsync(15_000);

    expect(await screen.findByText('LHB-PENDING-TERMINAL')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('External scan failed: radio failure'));
  });

  it('does not adopt a delayed start event whose scan already terminated untracked', async () => {
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(4, { stations: [createStation()] }));
    await Promise.resolve();
    await Promise.resolve();
    runtime.handlers.get('external-scan-started')?.(externalScanEvent(4));
    await Promise.resolve();

    expect(screen.queryByText('Preparing external scan...')).not.toBeInTheDocument();
    expect(screen.queryByText('External scan in progress...')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('still adopts a genuinely running external scan detected by polling', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValue(false);
    render(App);
    await screen.findByText('LHB-TEST');

    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true);
    await vi.advanceTimersByTimeAsync(15_000);

    expect(screen.getByText('Preparing external scan...')).toBeInTheDocument();
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
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockResolvedValueOnce(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-RECOVERED' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: ['partial metadata'] });
    render(App);
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled());

    await vi.advanceTimersByTimeAsync(15_000);

    expect(await screen.findByText('LHB-RECOVERED')).toBeInTheDocument();
    expect(screen.getByText('External scan completed: found 1; 1 known station. partial metadata')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('self-recovers a cancelled external scan without reporting completion', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockResolvedValueOnce(false);
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

  it('does not replay recovery for a stopped adopted scan when its delayed terminal arrives', async () => {
    vi.useFakeTimers();
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockResolvedValue(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-ADOPTED' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    api.CheckAllStationStatuses.mockResolvedValue([createStation({ name: 'LHB-ADOPTED' })]);
    render(App);
    await vi.waitFor(() => expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled());

    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    expect(await screen.findByText('External scan stopped.')).toBeInTheDocument();

    // The backend emits the stopped scan's terminal event after StopScan
    // settles. The stop already applied the outcome, so the delayed event must
    // be consumed: remembering it would make the next poll replay a full
    // recovery (clearing in-flight operations and re-reading the fleet) over
    // state that is already terminal.
    api.GetCurrentStationInfo.mockClear();
    api.GetScanStatus.mockClear();
    runtime.handlers.get('external-scan-cancelled')?.(externalScanEvent(3));
    await Promise.resolve();

    await vi.advanceTimersByTimeAsync(15_000);
    await vi.waitFor(() => expect(api.CheckAllStationStatuses).toHaveBeenCalledOnce());
    expect(api.GetCurrentStationInfo).not.toHaveBeenCalled();
    expect(screen.getByText('LHB-ADOPTED')).toBeInTheDocument();
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
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockImplementation(() => new Promise((resolve) => {
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
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValueOnce(true)
      .mockResolvedValueOnce(true).mockResolvedValue(false);
    api.GetCurrentStationInfo.mockResolvedValue([createStation({ name: 'LHB-RECOVERED-NEW' })]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(7));
    runtime.handlers.get('external-scan-completed')?.(externalScanEvent(7, { stations: [createStation()] }));
    await screen.findByText('External scan completed: found 1; 1 known station.');
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
    expect(screen.getByRole('status')).toHaveTextContent('Found 1; 1 known station.');

    resolveExternalList([createStation({ name: 'LHB-STALE-EXTERNAL' })]);
    await vi.advanceTimersByTimeAsync(15_000);

    expect(screen.queryByText('LHB-STALE-EXTERNAL')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Found 1; 1 known station.');
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
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockResolvedValue(false);
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
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockResolvedValue(false);
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
    api.IsScanning.mockResolvedValueOnce(true).mockResolvedValueOnce(true).mockResolvedValue(false);
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

  it('returns the header to Scan once a stopped local scan ends, even mid-stop', async () => {
    let rejectScan!: (error: Error) => void;
    let resolveStop!: () => void;
    api.ScanAndFetchStations.mockReturnValue(new Promise((_, reject) => { rejectScan = reject; }));
    api.StopScan.mockReturnValue(new Promise<void>((resolve) => { resolveStop = resolve; }));
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);
    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    expect(await screen.findByRole('button', { name: 'Stopping...' })).toBeDisabled();

    rejectScan(new Error('scan cancelled'));
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();
    resolveStop();
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

  it('recovers the header when a local StopScan times out and the scan promise stays pending', async () => {
    vi.useFakeTimers();
    // The scan promise never settles (the backend call is wedged), StopScan
    // reports its bounded timeout, and the authoritative probe afterwards
    // shows no scan running. Without the bounded recheck chain the header
    // would stay pinned on "Stopping..." because the periodic poll is blocked
    // by the still-loading local scan.
    api.ScanAndFetchStations.mockReturnValue(new Promise(() => {}));
    api.StopScan.mockRejectedValue(new Error('Scan stop timed out'));
    api.IsScanning.mockResolvedValue(false);
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    render(App);

    await fireEvent.click(await screen.findByRole('button', { name: 'Stop' }));
    await vi.waitFor(() => expect(api.StopScan).toHaveBeenCalledOnce());
    expect(screen.getByRole('status')).toHaveTextContent('Stopping scan...');

    await vi.advanceTimersByTimeAsync(1600);
    await vi.waitFor(() => expect(api.IsScanning).toHaveBeenCalled());
    expect(await screen.findByRole('button', { name: 'Stop' })).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Stopping...' })).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveTextContent('Scan stopped.');
  });

  it('force-settles a wedged local scan via the watchdog instead of staying stuck', async () => {
    vi.useFakeTimers();
    // The scan promise never settles (the backend call is wedged behind an
    // adapter call that ignores cancellation). Without the watchdog the UI
    // would stay in "Scanning..." forever: periodic polling is gated by the
    // still-loading local scan and only the promise's finally clears it.
    api.ScanAndFetchStations.mockReturnValue(new Promise(() => {}));
    api.StopScan.mockResolvedValue(undefined);
    // Startup probe sees no scan; the watchdog then sees the backend still
    // scanning once (requests a stop), then finished (settles the UI).
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValueOnce(true).mockResolvedValue(false);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
    render(App);
    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledOnce());
    expect(screen.getByRole('status')).toHaveTextContent('Scanning for base stations...');

    // Cross the watchdog window; it must request a stop of the wedged scan.
    await vi.advanceTimersByTimeAsync(90_000);
    await vi.waitFor(() => expect(api.StopScan).toHaveBeenCalledOnce());

    // The next recheck observes no scan running and force-settles the state,
    // restoring the Scan control and reporting the backend's real outcome.
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.waitFor(() => expect(api.IsScanning).toHaveBeenCalledTimes(3));
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();
    expect(screen.queryByText('Scanning for base stations...')).not.toBeInTheDocument();
  });

  it('starts a fresh scan after the watchdog force-settles a wedged one', async () => {
    vi.useFakeTimers();
    // The wedged promise never settles; the second scan succeeds. The wedged
    // promise's late finally must not corrupt the new scan's state (epoch
    // guard), and the new scan must both start and render its results.
    api.ScanAndFetchStations
      .mockReturnValueOnce(new Promise(() => {}))
      .mockResolvedValueOnce([createStation({ name: 'LHB-RESCANNED' })]);
    api.StopScan.mockResolvedValue(undefined);
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValueOnce(true).mockResolvedValue(false);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
    render(App);
    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledOnce());

    await vi.advanceTimersByTimeAsync(90_000);
    await vi.waitFor(() => expect(api.StopScan).toHaveBeenCalledOnce());
    await vi.advanceTimersByTimeAsync(5_000);
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();

    await fireEvent.click(screen.getByRole('button', { name: 'Scan' }));
    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('LHB-RESCANNED')).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('keeps the successor scan watchdog alive when a wedged predecessor settles late', async () => {
    vi.useFakeTimers();
    // Both scans wedge; the first is force-settled by its watchdog. When the
    // successor starts and the predecessor's promise finally settles late,
    // the predecessor's finally must not cancel the successor's watchdog:
    // that watchdog is the only recovery left for the wedged successor.
    let resolveFirstScan!: (stations: StationInfo[]) => void;
    api.ScanAndFetchStations
      .mockReturnValueOnce(new Promise((resolve) => { resolveFirstScan = resolve; }))
      .mockReturnValueOnce(new Promise(() => {}));
    api.StopScan.mockResolvedValue(undefined);
    api.IsScanning.mockResolvedValueOnce(false) // startup probe
      .mockResolvedValueOnce(true)              // first watchdog observes the scan
      .mockResolvedValueOnce(false)             // first watchdog recheck settles it
      .mockResolvedValueOnce(true)              // successor watchdog observes the scan
      .mockResolvedValueOnce(false);            // successor watchdog recheck settles it
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
    render(App);
    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledOnce());

    await vi.advanceTimersByTimeAsync(90_000);
    await vi.waitFor(() => expect(api.StopScan).toHaveBeenCalledOnce());
    await vi.advanceTimersByTimeAsync(5_000);
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();

    await fireEvent.click(screen.getByRole('button', { name: 'Scan' }));
    await vi.waitFor(() => expect(api.ScanAndFetchStations).toHaveBeenCalledTimes(2));
    // The wedged predecessor settles after the successor armed its watchdog.
    resolveFirstScan([]);
    await vi.advanceTimersByTimeAsync(0);

    await vi.advanceTimersByTimeAsync(90_000);
    await vi.waitFor(() => expect(api.StopScan).toHaveBeenCalledTimes(2));
    await vi.advanceTimersByTimeAsync(5_000);
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

    // Once the superseded scan ends the header returns to an actionable Scan
    // even though the old StopScan promise is still settling. The pending stop
    // belongs to the superseded scan, so the new scan must start in its own
    // running state with a working Stop control.
    await fireEvent.click(await screen.findByRole('button', { name: 'Scan' }));
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

  it('does not expose a Stop for an auto-sleep scan once the stop recheck budget expires', async () => {
    vi.useFakeTimers();
    // The stop is accepted but the scan keeps running; the recheck chain then
    // keeps observing a scanning backend.
    api.IsScanning.mockResolvedValueOnce(false).mockResolvedValue(true);
    api.StopScan.mockResolvedValue(undefined);
    api.GetScanStatus.mockResolvedValue({ state: 'running', found: 0, warnings: [] });
    api.GetCurrentStationInfo.mockResolvedValue([createStation()]);
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('external-scan-started')?.(externalScanEvent(9));
    await screen.findByText('Preparing external scan...');
    await fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    // The stop is in flight before the chain runs its course.
    await screen.findByRole('button', { name: 'Stopping...' });

    // Auto-sleep starts while the recheck chain runs; its internal scan is the
    // one still observed running.
    runtime.handlers.get('auto-sleep')?.({ phase: 'started' });

    // Exhaust the 10-attempt recheck chain. The header must come back to a
    // Scan button (not a Stop): the external-scan flag is dropped because the
    // scanning backend is the auto-sleep action's internal scan, which must
    // not be offered a Stop. Scan stays rendered but locked while auto-sleep
    // holds the adapter lock.
    await vi.advanceTimersByTimeAsync(16_000);
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeInTheDocument();
    expect(screen.queryByText('External scan in progress...')).not.toBeInTheDocument();
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

});
