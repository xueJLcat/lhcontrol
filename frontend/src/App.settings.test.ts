import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from './lib/types';
import { pushToast } from './lib/toast';

const api = vi.hoisted(() => ({
  CheckAllStationStatuses: vi.fn(),
  CancelBulkPower: vi.fn(),
  GetAbsentStationRetryLimit: vi.fn(),
  GetAPIListenAddress: vi.fn(),
  GetAPIStatus: vi.fn(),
  GetAutoSleepSettings: vi.fn(),
  GetBluetoothInitRetrySeconds: vi.fn(),
  GetBootFallbackSeconds: vi.fn(),
  GetBulkPowerTimeoutSeconds: vi.fn(),
  GetChannelConfirmAttempts: vi.fn(),
  GetChannelConfirmIntervalMs: vi.fn(),
  GetChannelScanFreshnessSeconds: vi.fn(),
  GetConfirmReconnectDelayMs: vi.fn(),
  GetConfirmReconnectThreshold: vi.fn(),
  GetDiscoveryAttempts: vi.fn(),
  GetDiscoveryRetryDelayMs: vi.fn(),
  GetIdentifyAttempts: vi.fn(),
  GetInitialReadTimeoutSeconds: vi.fn(),
  GetOperationRetryDelayMs: vi.fn(),
  GetPowerConfirmAttemptsOff: vi.fn(),
  GetPowerConfirmAttemptsOn: vi.fn(),
  GetPowerConfirmPollIntervalMs: vi.fn(),
  GetPowerWriteAttempts: vi.fn(),
  GetPresenceMissThreshold: vi.fn(),
  GetRecoveryRetryBaseSeconds: vi.fn(),
  GetRecoveryRetryMaxSeconds: vi.fn(),
  GetScanDurationSeconds: vi.fn(),
  GetScanOnStartup: vi.fn(),
  GetScanReadPhaseTimeoutSeconds: vi.fn(),
  GetSleepFinalWriteTimeoutSeconds: vi.fn(),
  GetSleepPrepareGapMs: vi.fn(),
  GetStationOperationTimeoutSeconds: vi.fn(),
  GetStatusPollIntervalSeconds: vi.fn(),
  GetStatusPollingEnabled: vi.fn(),
  GetStatusReadTimeoutSeconds: vi.fn(),
  GetStatusRefreshTimeoutSeconds: vi.fn(),
  GetCurrentStationInfo: vi.fn(),
  GetScanStatus: vi.fn(),
  IdentifyStation: vi.fn(),
  IsScanning: vi.fn(),
  ListBluetoothAdapters: vi.fn(),
  RefreshStationCapabilities: vi.fn(),
  RenameStationByAddress: vi.fn(),
  ScanAndFetchStations: vi.fn(),
  SetAllStationsPowerDetailed: vi.fn(),
  SetAbsentStationRetryLimit: vi.fn(),
  SetAPIListenAddress: vi.fn(),
  SetAutoSleepSettings: vi.fn(),
  SetBluetoothInitRetrySeconds: vi.fn(),
  SetBootFallbackSeconds: vi.fn(),
  SetBulkPowerTimeoutSeconds: vi.fn(),
  SetChannelConfirmAttempts: vi.fn(),
  SetChannelConfirmIntervalMs: vi.fn(),
  SetChannelScanFreshnessSeconds: vi.fn(),
  SetConfirmReconnectDelayMs: vi.fn(),
  SetConfirmReconnectThreshold: vi.fn(),
  SetDiscoveryAttempts: vi.fn(),
  SetDiscoveryRetryDelayMs: vi.fn(),
  SetIdentifyAttempts: vi.fn(),
  SetInitialReadTimeoutSeconds: vi.fn(),
  SetOperationRetryDelayMs: vi.fn(),
  SetPowerConfirmAttemptsOff: vi.fn(),
  SetPowerConfirmAttemptsOn: vi.fn(),
  SetPowerConfirmPollIntervalMs: vi.fn(),
  SetPowerWriteAttempts: vi.fn(),
  SetPresenceMissThreshold: vi.fn(),
  SetRecoveryRetryBaseSeconds: vi.fn(),
  SetRecoveryRetryMaxSeconds: vi.fn(),
  SetScanDurationSeconds: vi.fn(),
  SetScanOnStartup: vi.fn(),
  SetScanReadPhaseTimeoutSeconds: vi.fn(),
  SetSleepFinalWriteTimeoutSeconds: vi.fn(),
  SetSleepPrepareGapMs: vi.fn(),
  SetStationOperationTimeoutSeconds: vi.fn(),
  SetStatusPollIntervalSeconds: vi.fn(),
  SetStatusPollingEnabled: vi.fn(),
  SetStatusReadTimeoutSeconds: vi.fn(),
  SetStatusRefreshTimeoutSeconds: vi.fn(),
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

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
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
  api.GetStatusPollingEnabled.mockResolvedValue(true);
  api.ScanAndFetchStations.mockResolvedValue([createStation()]);
  api.GetScanStatus.mockResolvedValue({ state: 'completed', found: 1, warnings: [] });
  api.GetCurrentStationInfo.mockResolvedValue([createStation()]);
  api.CheckAllStationStatuses.mockResolvedValue([createStation()]);
  api.StopScan.mockResolvedValue(undefined);
  api.ListBluetoothAdapters.mockResolvedValue([]);
  api.GetAPIListenAddress.mockResolvedValue('127.0.0.1:7575');
  api.GetAutoSleepSettings.mockResolvedValue({ enabled: false, target: 'steamvr', delaySeconds: 300 });
  api.GetBootFallbackSeconds.mockResolvedValue(8);
  api.GetBulkPowerTimeoutSeconds.mockResolvedValue(120);
  api.GetDiscoveryAttempts.mockResolvedValue(3);
  api.GetDiscoveryRetryDelayMs.mockResolvedValue(500);
  api.GetPowerConfirmAttemptsOff.mockResolvedValue(15);
  api.GetPowerConfirmAttemptsOn.mockResolvedValue(51);
  api.GetPowerConfirmPollIntervalMs.mockResolvedValue(200);
  api.GetScanDurationSeconds.mockResolvedValue(5);
  api.GetSleepFinalWriteTimeoutSeconds.mockResolvedValue(30);
  api.GetSleepPrepareGapMs.mockResolvedValue(50);
  api.GetStationOperationTimeoutSeconds.mockResolvedValue(30);
  api.GetStatusPollIntervalSeconds.mockResolvedValue(15);
  api.GetStatusReadTimeoutSeconds.mockResolvedValue(20);
  api.GetStatusRefreshTimeoutSeconds.mockResolvedValue(30);
  api.GetPowerWriteAttempts.mockResolvedValue(2);
  api.GetOperationRetryDelayMs.mockResolvedValue(500);
  api.GetIdentifyAttempts.mockResolvedValue(2);
  api.GetConfirmReconnectThreshold.mockResolvedValue(2);
  api.GetConfirmReconnectDelayMs.mockResolvedValue(250);
  api.GetChannelConfirmAttempts.mockResolvedValue(5);
  api.GetChannelConfirmIntervalMs.mockResolvedValue(250);
  api.GetPresenceMissThreshold.mockResolvedValue(2);
  api.GetInitialReadTimeoutSeconds.mockResolvedValue(30);
  api.GetScanReadPhaseTimeoutSeconds.mockResolvedValue(45);
  api.GetChannelScanFreshnessSeconds.mockResolvedValue(120);
  api.GetRecoveryRetryBaseSeconds.mockResolvedValue(30);
  api.GetRecoveryRetryMaxSeconds.mockResolvedValue(300);
  api.GetAbsentStationRetryLimit.mockResolvedValue(5);
  api.GetBluetoothInitRetrySeconds.mockResolvedValue(2);
  api.SetAPIListenAddress.mockResolvedValue(undefined);
  api.SetAbsentStationRetryLimit.mockResolvedValue(undefined);
  api.SetBluetoothInitRetrySeconds.mockResolvedValue(undefined);
  api.SetChannelConfirmAttempts.mockResolvedValue(undefined);
  api.SetChannelConfirmIntervalMs.mockResolvedValue(undefined);
  api.SetChannelScanFreshnessSeconds.mockResolvedValue(undefined);
  api.SetConfirmReconnectDelayMs.mockResolvedValue(undefined);
  api.SetConfirmReconnectThreshold.mockResolvedValue(undefined);
  api.SetIdentifyAttempts.mockResolvedValue(undefined);
  api.SetInitialReadTimeoutSeconds.mockResolvedValue(undefined);
  api.SetOperationRetryDelayMs.mockResolvedValue(undefined);
  api.SetPowerWriteAttempts.mockResolvedValue(undefined);
  api.SetPresenceMissThreshold.mockResolvedValue(undefined);
  api.SetRecoveryRetryBaseSeconds.mockResolvedValue(undefined);
  api.SetRecoveryRetryMaxSeconds.mockResolvedValue(undefined);
  api.SetScanReadPhaseTimeoutSeconds.mockResolvedValue(undefined);
  api.SetStatusReadTimeoutSeconds.mockResolvedValue(undefined);
  api.SetStatusRefreshTimeoutSeconds.mockResolvedValue(undefined);
  api.SetAutoSleepSettings.mockResolvedValue(undefined);
  api.SetBootFallbackSeconds.mockResolvedValue(undefined);
  api.SetBulkPowerTimeoutSeconds.mockResolvedValue(undefined);
  api.SetDiscoveryAttempts.mockResolvedValue(undefined);
  api.SetDiscoveryRetryDelayMs.mockResolvedValue(undefined);
  api.SetPowerConfirmAttemptsOff.mockResolvedValue(undefined);
  api.SetPowerConfirmAttemptsOn.mockResolvedValue(undefined);
  api.SetPowerConfirmPollIntervalMs.mockResolvedValue(undefined);
  api.SetScanDurationSeconds.mockResolvedValue(undefined);
  api.SetScanOnStartup.mockResolvedValue(undefined);
  api.SetSleepFinalWriteTimeoutSeconds.mockResolvedValue(undefined);
  api.SetSleepPrepareGapMs.mockResolvedValue(undefined);
  api.SetStationOperationTimeoutSeconds.mockResolvedValue(undefined);
  api.SetStatusPollIntervalSeconds.mockResolvedValue(undefined);
  api.SetStatusPollingEnabled.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('App settings drawer', () => {
  it('honors a startup-scan change made before the startup read settles', async () => {
    const startupPreference = deferred<boolean>();
    api.GetScanOnStartup
      .mockReturnValueOnce(startupPreference.promise)
      .mockResolvedValueOnce(true);
    render(App);

    await fireEvent.click(screen.getByRole('button', { name: 'Open settings' }));
    const toggle = await screen.findByRole('checkbox', { name: 'Scan when the application starts' });
    await fireEvent.click(toggle);
    await waitFor(() => expect(api.SetScanOnStartup).toHaveBeenCalledWith(false));

    startupPreference.resolve(true);
    await waitFor(() => expect(api.GetScanOnStartup).toHaveBeenCalledTimes(2));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(api.ScanAndFetchStations).not.toHaveBeenCalled();
  });

  it('opens the settings drawer and lists adapter diagnostics', async () => {
    api.ListBluetoothAdapters.mockResolvedValue([
      { deviceId: 'BT-1', name: 'Intel Wireless Bluetooth' },
      { deviceId: 'BT-2', name: 'CSR Dongle' }
    ]);
    render(App);
    await screen.findByText('LHB-TEST');

    await fireEvent.click(screen.getByRole('button', { name: 'Open settings' }));
    expect(await screen.findByRole('dialog', { name: 'Settings' })).toBeInTheDocument();
    expect(api.ListBluetoothAdapters).toHaveBeenCalledOnce();
    expect(screen.getByText('Intel Wireless Bluetooth')).toBeInTheDocument();
    expect(screen.getByText('BT-1')).toBeInTheDocument();
  });

  it('refreshes the published API address immediately after it is saved', async () => {
    render(App);
    await screen.findByText('LHB-TEST');
    const apiStatus = await screen.findByRole('button', { name: 'API ready' });
    expect(apiStatus).toHaveAttribute('title', 'HTTP API 127.0.0.1:7575');

    await fireEvent.click(screen.getByRole('button', { name: 'Open settings' }));
    const input = await screen.findByLabelText('Listen address');
    api.GetAPIStatus.mockResolvedValue({
      running: true,
      address: '127.0.0.1:8080',
      error: '',
      warnings: [],
      configWritable: true
    });
    await fireEvent.input(input, { target: { value: '127.0.0.1:8080' } });
    await fireEvent.change(input);

    await waitFor(() => expect(api.SetAPIListenAddress).toHaveBeenCalledWith('127.0.0.1:8080'));
    await waitFor(() => expect(apiStatus).toHaveAttribute('title', 'HTTP API 127.0.0.1:8080'));
  });

  it('closes the settings drawer with Escape', async () => {
    render(App);
    await screen.findByText('LHB-TEST');
    await fireEvent.click(screen.getByRole('button', { name: 'Open settings' }));
    await screen.findByRole('dialog', { name: 'Settings' });

    await fireEvent.keyDown(window, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Settings' })).not.toBeInTheDocument());
  });

  it('replaces the settings drawer when a station is selected', async () => {
    render(App);
    await screen.findByText('LHB-TEST');
    await fireEvent.click(screen.getByRole('button', { name: 'Open settings' }));
    await screen.findByRole('dialog', { name: 'Settings' });

    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Settings' })).not.toBeInTheDocument());
    expect(screen.getByRole('dialog', { name: 'Station details' })).toBeInTheDocument();
  });
});

describe('App auto sleep settings', () => {
  async function openSettings() {
    render(App);
    await screen.findByText('LHB-TEST');
    await fireEvent.click(screen.getByRole('button', { name: 'Open settings' }));
    await screen.findByRole('dialog', { name: 'Settings' });
  }

  it('loads and shows the auto sleep section when the drawer opens', async () => {
    await openSettings();
    await waitFor(() => expect(api.GetAutoSleepSettings).toHaveBeenCalledOnce());
    const toggle = await screen.findByRole('checkbox', { name: 'Enable auto sleep' });
    expect(toggle).not.toBeChecked();
  });

  it('enables auto sleep and persists the change', async () => {
    await openSettings();
    const toggle = await screen.findByRole('checkbox', { name: 'Enable auto sleep' });
    await fireEvent.click(toggle);
    await waitFor(() => expect(api.SetAutoSleepSettings).toHaveBeenCalledTimes(1));
    const saved = api.SetAutoSleepSettings.mock.calls[0][0];
    expect(saved.enabled).toBe(true);
    expect(saved.target).toBe('steamvr');
    expect(saved.delaySeconds).toBe(300);
  });

  it('rolls auto sleep settings back when saving fails', async () => {
    api.SetAutoSleepSettings.mockRejectedValue(new Error('config locked'));
    await openSettings();
    const toggle = await screen.findByRole('checkbox', { name: 'Enable auto sleep' });
    await fireEvent.click(toggle);
    await waitFor(() => expect(api.SetAutoSleepSettings).toHaveBeenCalledTimes(1));
    expect(pushToast).toHaveBeenCalledWith('Auto-sleep settings could not be saved: Error: config locked');
    const rolledBack = await screen.findByRole('checkbox', { name: 'Enable auto sleep' });
    expect((rolledBack as HTMLInputElement).checked).toBe(false);
  });

  it('switches the trigger target once enabled', async () => {
    api.GetAutoSleepSettings.mockResolvedValue({ enabled: true, target: 'steamvr', delaySeconds: 300 });
    await openSettings();
    const steamOption = await screen.findByText('steam.exe — fires only when the Steam client fully exits');
    await fireEvent.click(steamOption);
    await waitFor(() => expect(api.SetAutoSleepSettings).toHaveBeenCalledTimes(1));
    const saved = api.SetAutoSleepSettings.mock.calls[0][0];
    expect(saved.target).toBe('steam');
  });

  it('toasts auto-sleep progress events', async () => {
    render(App);
    await screen.findByText('LHB-TEST');

    runtime.handlers.get('auto-sleep')?.({ phase: 'started' });
    expect(pushToast).toHaveBeenCalledWith('Session ended — scanning and putting all stations to sleep.', 'info');

    runtime.handlers.get('auto-sleep')?.({ phase: 'completed', success: 2, failed: 0 });
    expect(pushToast).toHaveBeenCalledWith('Auto sleep finished: 2 station(s) put to sleep.', 'success');

    runtime.handlers.get('auto-sleep')?.({ phase: 'skipped', error: 'another Bluetooth operation is in progress' });
    expect(pushToast).toHaveBeenCalledWith('Auto sleep skipped: another Bluetooth operation is in progress.', 'info');

    runtime.handlers.get('auto-sleep')?.({ phase: 'failed', error: 'boom' });
    expect(pushToast).toHaveBeenCalledWith('Auto sleep failed: boom.');
  });
});
