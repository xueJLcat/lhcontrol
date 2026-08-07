import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  GetAutoSleepSettings: vi.fn(),
  GetBulkPowerTimeoutSeconds: vi.fn(),
  GetScanDurationSeconds: vi.fn(),
  GetScanOnStartup: vi.fn(),
  GetStatusPollIntervalSeconds: vi.fn(),
  GetStatusPollingEnabled: vi.fn(),
  ListBluetoothAdapters: vi.fn(),
  SetAutoSleepSettings: vi.fn(),
  SetBulkPowerTimeoutSeconds: vi.fn(),
  SetScanDurationSeconds: vi.fn(),
  SetScanOnStartup: vi.fn(),
  SetStatusPollIntervalSeconds: vi.fn(),
  SetStatusPollingEnabled: vi.fn(),
  SetLanguage: vi.fn()
}));

vi.mock('../backend', () => backend);
vi.mock('../toast', async (importOriginal) => {
  const original = await importOriginal<typeof import('../toast')>();
  return { ...original, pushToast: vi.fn() };
});

import SettingsPanel from './SettingsPanel.svelte';
import { pushToast } from '../toast';
import { languagePreference, setLanguagePreference } from '../i18n.svelte';

afterEach(cleanup);

beforeEach(() => {
  vi.clearAllMocks();
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    value: () => ({
      cancel: vi.fn(),
      finish: vi.fn(),
      play: vi.fn(),
      pause: vi.fn(),
      reverse: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      finished: Promise.resolve()
    })
  });
  backend.ListBluetoothAdapters.mockResolvedValue([
    { deviceId: 'BT-1', name: 'Intel Wireless Bluetooth' },
    { deviceId: 'BT-2', name: 'CSR Dongle' }
  ]);
  backend.GetAutoSleepSettings.mockResolvedValue({ enabled: false, target: 'steamvr', delaySeconds: 300 });
  backend.GetBulkPowerTimeoutSeconds.mockResolvedValue(120);
  backend.GetScanDurationSeconds.mockResolvedValue(5);
  backend.GetScanOnStartup.mockResolvedValue(true);
  backend.GetStatusPollIntervalSeconds.mockResolvedValue(15);
  backend.GetStatusPollingEnabled.mockResolvedValue(true);
  backend.SetAutoSleepSettings.mockResolvedValue(undefined);
  backend.SetBulkPowerTimeoutSeconds.mockResolvedValue(undefined);
  backend.SetScanDurationSeconds.mockResolvedValue(undefined);
  backend.SetScanOnStartup.mockResolvedValue(undefined);
  backend.SetStatusPollIntervalSeconds.mockResolvedValue(undefined);
  backend.SetStatusPollingEnabled.mockResolvedValue(undefined);
  backend.SetLanguage.mockResolvedValue(undefined);
  setLanguagePreference('system');
});

describe('SettingsPanel', () => {
  it('loads adapter and auto-sleep settings on mount', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    expect(await screen.findByRole('dialog', { name: 'Settings' })).toBeInTheDocument();
    expect(backend.ListBluetoothAdapters).toHaveBeenCalledOnce();
    expect(backend.GetAutoSleepSettings).toHaveBeenCalledOnce();
    expect(backend.GetBulkPowerTimeoutSeconds).toHaveBeenCalledOnce();
    expect(backend.GetScanDurationSeconds).toHaveBeenCalledOnce();
    expect(backend.GetScanOnStartup).toHaveBeenCalledOnce();
    expect(backend.GetStatusPollIntervalSeconds).toHaveBeenCalledOnce();
    expect(backend.GetStatusPollingEnabled).toHaveBeenCalledOnce();
    expect(await screen.findByText('Intel Wireless Bluetooth')).toBeInTheDocument();
    expect(screen.getByText('BT-1')).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Enable auto sleep' })).not.toBeChecked();
    expect(screen.getByLabelText('Bulk power timeout')).toHaveValue(120);
    expect(screen.getByLabelText('Status polling interval')).toHaveValue(15);
    expect(screen.getByRole('checkbox', { name: 'Scan when the application starts' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Refresh station status automatically' })).toBeChecked();
    expect(screen.getByLabelText('Bluetooth scan duration')).toHaveValue(5);
  });

  it('persists the bulk power timeout', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Bulk power timeout');
    await fireEvent.input(input, { target: { value: '180' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetBulkPowerTimeoutSeconds).toHaveBeenCalledWith(180));
  });

  it('persists scan and automatic station refresh preferences', async () => {
    const onStatusPollingEnabledChanged = vi.fn();
    render(SettingsPanel, { props: { onClose: vi.fn(), onStatusPollingEnabledChanged } });

    await fireEvent.click(await screen.findByRole('checkbox', { name: 'Scan when the application starts' }));
    await waitFor(() => expect(backend.SetScanOnStartup).toHaveBeenCalledWith(false));

    const duration = screen.getByLabelText('Bluetooth scan duration');
    await fireEvent.input(duration, { target: { value: '12' } });
    await fireEvent.change(duration);
    await waitFor(() => expect(backend.SetScanDurationSeconds).toHaveBeenCalledWith(12));

    await fireEvent.click(screen.getByRole('checkbox', { name: 'Refresh station status automatically' }));
    await waitFor(() => expect(backend.SetStatusPollingEnabled).toHaveBeenCalledWith(false));
    expect(onStatusPollingEnabledChanged).toHaveBeenCalledWith(false);
  });

  it('rolls the bulk power timeout back when saving fails', async () => {
    backend.SetBulkPowerTimeoutSeconds.mockRejectedValue(new Error('config locked'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Bulk power timeout');
    await fireEvent.input(input, { target: { value: '180' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetBulkPowerTimeoutSeconds).toHaveBeenCalledWith(180));
    await waitFor(() => expect(input).toHaveValue(120));
    expect(pushToast).toHaveBeenCalledWith('Bulk power timeout could not be saved: Error: config locked');
  });

  it('persists the status polling interval and applies it immediately', async () => {
    const onStatusPollIntervalChanged = vi.fn();
    render(SettingsPanel, { props: { onClose: vi.fn(), onStatusPollIntervalChanged } });
    const input = await screen.findByLabelText('Status polling interval');
    await fireEvent.input(input, { target: { value: '45' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetStatusPollIntervalSeconds).toHaveBeenCalledWith(45));
    expect(onStatusPollIntervalChanged).toHaveBeenCalledWith(45);
  });

  it('rolls the status polling interval back when saving fails', async () => {
    backend.SetStatusPollIntervalSeconds.mockRejectedValue(new Error('config locked'));
    const onStatusPollIntervalChanged = vi.fn();
    render(SettingsPanel, { props: { onClose: vi.fn(), onStatusPollIntervalChanged } });
    const input = await screen.findByLabelText('Status polling interval');
    await fireEvent.input(input, { target: { value: '45' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetStatusPollIntervalSeconds).toHaveBeenCalledWith(45));
    await waitFor(() => expect(input).toHaveValue(15));
    expect(onStatusPollIntervalChanged).not.toHaveBeenCalled();
    expect(pushToast).toHaveBeenCalledWith('Status polling interval could not be saved: Error: config locked');
  });

  it('persists auto sleep changes', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'Enable auto sleep' }));
    await waitFor(() => expect(backend.SetAutoSleepSettings).toHaveBeenCalledTimes(1));
    const saved = backend.SetAutoSleepSettings.mock.calls[0][0];
    expect(saved.enabled).toBe(true);
    expect(saved.target).toBe('steamvr');
    expect(saved.delaySeconds).toBe(300);
  });

  it('rolls auto sleep settings back when saving fails', async () => {
    backend.SetAutoSleepSettings.mockRejectedValue(new Error('config locked'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(await screen.findByRole('checkbox', { name: 'Enable auto sleep' }));
    await waitFor(() => expect(backend.SetAutoSleepSettings).toHaveBeenCalledTimes(1));
    expect(pushToast).toHaveBeenCalledWith('Auto-sleep settings could not be saved: Error: config locked');
    const rolledBack = await screen.findByRole('checkbox', { name: 'Enable auto sleep' });
    expect((rolledBack as HTMLInputElement).checked).toBe(false);
  });

  it('forwards close requests', async () => {
    const onClose = vi.fn();
    render(SettingsPanel, { props: { onClose } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(screen.getByRole('button', { name: 'Close settings' }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('switches language immediately and persists it', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(screen.getByRole('radio', { name: '简体中文' }));
    expect(await screen.findByRole('dialog', { name: '设置' })).toBeInTheDocument();
    await waitFor(() => expect(backend.SetLanguage).toHaveBeenCalledWith('zh-CN'));
  });

  it('can explicitly persist the language that currently matches the system', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(screen.getByRole('radio', { name: 'English' }));
    await waitFor(() => expect(backend.SetLanguage).toHaveBeenCalledWith('en'));
    expect(languagePreference()).toBe('en');
  });

  it('can return to following the system language', async () => {
    setLanguagePreference('zh-CN');
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: '设置' });
    await fireEvent.click(screen.getByRole('radio', { name: '跟随系统' }));
    await waitFor(() => expect(backend.SetLanguage).toHaveBeenCalledWith(''));
    expect(languagePreference()).toBe('system');
  });

  it('restores the previous language when saving fails', async () => {
    backend.SetLanguage.mockRejectedValue(new Error('config locked'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(screen.getByRole('radio', { name: '简体中文' }));
    await waitFor(() => expect(backend.SetLanguage).toHaveBeenCalledOnce());
    expect(await screen.findByRole('dialog', { name: 'Settings' })).toBeInTheDocument();
    expect(languagePreference()).toBe('system');
    expect(pushToast).toHaveBeenCalledWith('Language setting could not be saved: Error: config locked');
  });
});
