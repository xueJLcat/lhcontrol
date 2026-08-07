import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from './lib/types';
import { pushToast } from './lib/toast';

const api = vi.hoisted(() => ({
  CheckAllStationStatuses: vi.fn(),
  GetAPIStatus: vi.fn(),
  GetAutoSleepSettings: vi.fn(),
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
  it('excludes unverified power states from fleet counts', async () => {
    api.ScanAndFetchStations.mockResolvedValue([
      createStation({ name: 'VERIFIED', address: 'AA', powerState: 1, powerStateName: 'on', rawPowerState: 0x0b }),
      createStation({ name: 'UNVERIFIED', address: 'BB', powerState: 1, powerStateName: 'on', powerStateConfirmed: false })
    ]);
    render(App);
    await screen.findByText('UNVERIFIED');
    expect(screen.getByText('1 On')).toBeInTheDocument();
    expect(screen.queryByText('2 On')).not.toBeInTheDocument();
  });

  it('switches the header to Stop while an external scan runs over an empty fleet', async () => {
    api.ScanAndFetchStations.mockResolvedValue([]);
    api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 0, warnings: [] });

    render(App);
    expect(await screen.findByRole('button', { name: 'Scan' })).toBeEnabled();
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

  it('shows an actionable recovery card above the grid when a scan fails', async () => {
    api.ScanAndFetchStations.mockRejectedValue(new Error('Bluetooth is unavailable; turn on Bluetooth and retry'));
    api.GetCurrentStationInfo.mockResolvedValue([
      createStation({ name: 'LHB-LATEST', connectionState: 'disconnected' })
    ]);
    render(App);
    expect(await screen.findByRole('heading', { name: 'Bluetooth is unavailable' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(await screen.findByText('LHB-LATEST')).toBeInTheDocument();
    expect(screen.queryByText('No base stations found.')).not.toBeInTheDocument();
  });

  it('keeps the recovery card until the next scan clears it', async () => {
    api.ScanAndFetchStations
      .mockRejectedValueOnce(new Error('operation timed out'))
      .mockResolvedValue([createStation()]);
    api.GetCurrentStationInfo.mockResolvedValue([]);

    render(App);
    expect(await screen.findByRole('heading', { name: 'The scan timed out' })).toBeInTheDocument();
    expect(screen.queryByText('No base stations found.')).not.toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: 'Scan' }));
    expect(await screen.findByText('LHB-TEST')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(api.ScanAndFetchStations).toHaveBeenCalledTimes(2);
  });

  it('keeps the recovery card from claiming a scan that merely stopped', async () => {
    api.GetScanStatus.mockResolvedValue({ state: 'cancelled', found: 0, warnings: [] });
    api.ScanAndFetchStations.mockRejectedValue(new Error('scan cancelled'));
    api.GetCurrentStationInfo.mockResolvedValue([]);
    render(App);

    expect(await screen.findByText('Scan stopped.')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Scan' })).toBeEnabled();
  });

  it('demands confirmation before a bulk command that includes unverified stations', async () => {
    const trusted = createStation({ name: 'LHB-TRUSTED', address: 'AA' });
    const stale = createStation({ name: 'LHB-STALE', address: 'BB', isPresent: false });
    api.ScanAndFetchStations.mockResolvedValue([trusted, stale]);
    api.GetCurrentStationInfo.mockResolvedValue([trusted, stale]);
    api.SetAllStationsPowerDetailed.mockResolvedValue({ target: 'on', results: [] });

    render(App);
    await screen.findByText('LHB-STALE');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());

    expect(screen.getByText(/not fully verified — bulk commands include them/)).toBeInTheDocument();
    await fireEvent.click(screen.getByTitle('Turn all known stations on'));
    expect(api.SetAllStationsPowerDetailed).not.toHaveBeenCalled();

	const dialog = await screen.findByRole('dialog', { name: 'Confirm bulk power' });
	expect(document.querySelector('.app-container')).toHaveProperty('inert', true);
	expect(within(dialog).getByText('Visible & verified')).toBeInTheDocument();
    expect(within(dialog).getByText('Not seen in latest scan')).toBeInTheDocument();
    await fireEvent.click(within(dialog).getByRole('button', { name: /Turn on/ }));
    await waitFor(() => expect(api.SetAllStationsPowerDetailed).toHaveBeenCalledWith('on'));
  });

  it('cancels the bulk confirmation through the dialog, scrim and Escape key', async () => {
    const trusted = createStation({ name: 'LHB-TRUSTED', address: 'AA' });
    const stale = createStation({ name: 'LHB-STALE', address: 'BB', isPresent: false });
    api.ScanAndFetchStations.mockResolvedValue([trusted, stale]);
    api.GetCurrentStationInfo.mockResolvedValue([trusted, stale]);

    render(App);
    await screen.findByText('LHB-STALE');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Scan' })).not.toBeDisabled());

    await fireEvent.click(screen.getByTitle('Turn all known stations on'));
    await fireEvent.click(within(await screen.findByRole('dialog', { name: 'Confirm bulk power' }))
      .getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Confirm bulk power' })).not.toBeInTheDocument());

    await fireEvent.click(screen.getByTitle('Turn all known stations on'));
    await screen.findByRole('dialog', { name: 'Confirm bulk power' });
    await fireEvent.keyDown(window, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Confirm bulk power' })).not.toBeInTheDocument());
    expect(api.SetAllStationsPowerDetailed).not.toHaveBeenCalled();
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
