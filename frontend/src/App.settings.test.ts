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
  GetBulkPowerTimeoutSeconds: vi.fn(),
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
  SetBulkPowerTimeoutSeconds: vi.fn(),
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
  api.GetBulkPowerTimeoutSeconds.mockResolvedValue(120);
  api.SetAutoSleepSettings.mockResolvedValue(undefined);
  api.SetBulkPowerTimeoutSeconds.mockResolvedValue(undefined);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('App settings drawer', () => {
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
