import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  GetAbsentStationRetryLimit: vi.fn(),
  GetAPIListenAddress: vi.fn(),
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
  ListBluetoothAdapters: vi.fn(),
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
  backend.GetAPIListenAddress.mockResolvedValue('127.0.0.1:7575');
  backend.GetAutoSleepSettings.mockResolvedValue({ enabled: false, target: 'steamvr', delaySeconds: 300 });
  backend.GetBootFallbackSeconds.mockResolvedValue(8);
  backend.GetBulkPowerTimeoutSeconds.mockResolvedValue(120);
  backend.GetDiscoveryAttempts.mockResolvedValue(3);
  backend.GetDiscoveryRetryDelayMs.mockResolvedValue(500);
  backend.GetPowerConfirmAttemptsOff.mockResolvedValue(15);
  backend.GetPowerConfirmAttemptsOn.mockResolvedValue(51);
  backend.GetPowerConfirmPollIntervalMs.mockResolvedValue(200);
  backend.GetScanDurationSeconds.mockResolvedValue(5);
  backend.GetScanOnStartup.mockResolvedValue(true);
  backend.GetSleepFinalWriteTimeoutSeconds.mockResolvedValue(30);
  backend.GetSleepPrepareGapMs.mockResolvedValue(50);
  backend.GetStationOperationTimeoutSeconds.mockResolvedValue(30);
  backend.GetStatusPollIntervalSeconds.mockResolvedValue(15);
  backend.GetStatusPollingEnabled.mockResolvedValue(true);
  backend.SetAPIListenAddress.mockResolvedValue(undefined);
  backend.SetAutoSleepSettings.mockResolvedValue(undefined);
  backend.SetBootFallbackSeconds.mockResolvedValue(undefined);
  backend.SetBulkPowerTimeoutSeconds.mockResolvedValue(undefined);
  backend.SetDiscoveryAttempts.mockResolvedValue(undefined);
  backend.SetDiscoveryRetryDelayMs.mockResolvedValue(undefined);
  backend.SetPowerConfirmAttemptsOff.mockResolvedValue(undefined);
  backend.SetPowerConfirmAttemptsOn.mockResolvedValue(undefined);
  backend.SetPowerConfirmPollIntervalMs.mockResolvedValue(undefined);
  backend.SetScanDurationSeconds.mockResolvedValue(undefined);
  backend.SetScanOnStartup.mockResolvedValue(undefined);
  backend.SetSleepFinalWriteTimeoutSeconds.mockResolvedValue(undefined);
  backend.SetSleepPrepareGapMs.mockResolvedValue(undefined);
  backend.SetStationOperationTimeoutSeconds.mockResolvedValue(undefined);
  backend.GetPowerWriteAttempts.mockResolvedValue(2);
  backend.GetOperationRetryDelayMs.mockResolvedValue(500);
  backend.GetIdentifyAttempts.mockResolvedValue(2);
  backend.GetConfirmReconnectThreshold.mockResolvedValue(2);
  backend.GetConfirmReconnectDelayMs.mockResolvedValue(250);
  backend.GetChannelConfirmAttempts.mockResolvedValue(5);
  backend.GetChannelConfirmIntervalMs.mockResolvedValue(250);
  backend.GetPresenceMissThreshold.mockResolvedValue(2);
  backend.GetInitialReadTimeoutSeconds.mockResolvedValue(30);
  backend.GetScanReadPhaseTimeoutSeconds.mockResolvedValue(45);
  backend.GetStatusReadTimeoutSeconds.mockResolvedValue(20);
  backend.GetStatusRefreshTimeoutSeconds.mockResolvedValue(30);
  backend.GetChannelScanFreshnessSeconds.mockResolvedValue(120);
  backend.GetRecoveryRetryBaseSeconds.mockResolvedValue(30);
  backend.GetRecoveryRetryMaxSeconds.mockResolvedValue(300);
  backend.GetAbsentStationRetryLimit.mockResolvedValue(5);
  backend.GetBluetoothInitRetrySeconds.mockResolvedValue(2);
  backend.SetStatusPollIntervalSeconds.mockResolvedValue(undefined);
  backend.SetStatusPollingEnabled.mockResolvedValue(undefined);
  backend.SetPowerWriteAttempts.mockResolvedValue(undefined);
  backend.SetOperationRetryDelayMs.mockResolvedValue(undefined);
  backend.SetIdentifyAttempts.mockResolvedValue(undefined);
  backend.SetConfirmReconnectThreshold.mockResolvedValue(undefined);
  backend.SetConfirmReconnectDelayMs.mockResolvedValue(undefined);
  backend.SetChannelConfirmAttempts.mockResolvedValue(undefined);
  backend.SetChannelConfirmIntervalMs.mockResolvedValue(undefined);
  backend.SetPresenceMissThreshold.mockResolvedValue(undefined);
  backend.SetInitialReadTimeoutSeconds.mockResolvedValue(undefined);
  backend.SetScanReadPhaseTimeoutSeconds.mockResolvedValue(undefined);
  backend.SetStatusReadTimeoutSeconds.mockResolvedValue(undefined);
  backend.SetStatusRefreshTimeoutSeconds.mockResolvedValue(undefined);
  backend.SetChannelScanFreshnessSeconds.mockResolvedValue(undefined);
  backend.SetRecoveryRetryBaseSeconds.mockResolvedValue(undefined);
  backend.SetRecoveryRetryMaxSeconds.mockResolvedValue(undefined);
  backend.SetAbsentStationRetryLimit.mockResolvedValue(undefined);
  backend.SetBluetoothInitRetrySeconds.mockResolvedValue(undefined);
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
    expect(backend.GetStationOperationTimeoutSeconds).toHaveBeenCalledOnce();
    expect(backend.GetPowerConfirmAttemptsOn).toHaveBeenCalledOnce();
    expect(backend.GetPowerConfirmAttemptsOff).toHaveBeenCalledOnce();
    expect(backend.GetPowerConfirmPollIntervalMs).toHaveBeenCalledOnce();
    expect(backend.GetBootFallbackSeconds).toHaveBeenCalledOnce();
    expect(backend.GetSleepFinalWriteTimeoutSeconds).toHaveBeenCalledOnce();
    expect(backend.GetSleepPrepareGapMs).toHaveBeenCalledOnce();
    expect(backend.GetDiscoveryAttempts).toHaveBeenCalledOnce();
    expect(backend.GetDiscoveryRetryDelayMs).toHaveBeenCalledOnce();
    expect(backend.GetAPIListenAddress).toHaveBeenCalledOnce();
    expect(backend.GetPowerWriteAttempts).toHaveBeenCalledOnce();
    expect(backend.GetOperationRetryDelayMs).toHaveBeenCalledOnce();
    expect(backend.GetIdentifyAttempts).toHaveBeenCalledOnce();
    expect(backend.GetConfirmReconnectThreshold).toHaveBeenCalledOnce();
    expect(backend.GetConfirmReconnectDelayMs).toHaveBeenCalledOnce();
    expect(backend.GetChannelConfirmAttempts).toHaveBeenCalledOnce();
    expect(backend.GetChannelConfirmIntervalMs).toHaveBeenCalledOnce();
    expect(backend.GetPresenceMissThreshold).toHaveBeenCalledOnce();
    expect(backend.GetInitialReadTimeoutSeconds).toHaveBeenCalledOnce();
    expect(backend.GetScanReadPhaseTimeoutSeconds).toHaveBeenCalledOnce();
    expect(backend.GetStatusReadTimeoutSeconds).toHaveBeenCalledOnce();
    expect(backend.GetStatusRefreshTimeoutSeconds).toHaveBeenCalledOnce();
    expect(backend.GetChannelScanFreshnessSeconds).toHaveBeenCalledOnce();
    expect(backend.GetRecoveryRetryBaseSeconds).toHaveBeenCalledOnce();
    expect(backend.GetRecoveryRetryMaxSeconds).toHaveBeenCalledOnce();
    expect(backend.GetAbsentStationRetryLimit).toHaveBeenCalledOnce();
    expect(backend.GetBluetoothInitRetrySeconds).toHaveBeenCalledOnce();
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

  it('persists the station operation timeout', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Station operation timeout');
    await fireEvent.input(input, { target: { value: '90' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetStationOperationTimeoutSeconds).toHaveBeenCalledWith(90));
  });

  it('rolls the station operation timeout back when saving fails', async () => {
    backend.SetStationOperationTimeoutSeconds.mockRejectedValue(new Error('bulk timeout is smaller'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Station operation timeout');
    await fireEvent.input(input, { target: { value: '90' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetStationOperationTimeoutSeconds).toHaveBeenCalledWith(90));
    await waitFor(() => expect(input).toHaveValue(30));
    expect(pushToast).toHaveBeenCalledWith('Station operation timeout could not be saved: Error: bulk timeout is smaller');
  });

  it('persists power confirmation timing settings', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const attemptsOn = await screen.findByLabelText('Power-on confirmation attempts');
    await fireEvent.input(attemptsOn, { target: { value: '25' } });
    await fireEvent.change(attemptsOn);
    await waitFor(() => expect(backend.SetPowerConfirmAttemptsOn).toHaveBeenCalledWith(25));

    const attemptsOff = screen.getByLabelText('Sleep/standby confirmation attempts');
    await fireEvent.input(attemptsOff, { target: { value: '9' } });
    await fireEvent.change(attemptsOff);
    await waitFor(() => expect(backend.SetPowerConfirmAttemptsOff).toHaveBeenCalledWith(9));

    const pollInterval = screen.getByLabelText('Confirmation read interval');
    await fireEvent.input(pollInterval, { target: { value: '400' } });
    await fireEvent.change(pollInterval);
    await waitFor(() => expect(backend.SetPowerConfirmPollIntervalMs).toHaveBeenCalledWith(400));

    const bootFallback = screen.getByLabelText('Boot fallback window');
    await fireEvent.input(bootFallback, { target: { value: '20' } });
    await fireEvent.change(bootFallback);
    await waitFor(() => expect(backend.SetBootFallbackSeconds).toHaveBeenCalledWith(20));
  });

  it('persists advanced recovery settings', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const retryBase = await screen.findByLabelText('Recovery retry base');
    await fireEvent.input(retryBase, { target: { value: '60' } });
    await fireEvent.change(retryBase);
    await waitFor(() => expect(backend.SetRecoveryRetryBaseSeconds).toHaveBeenCalledWith(60));

    const retryLimit = screen.getByLabelText('Absent station retry limit');
    await fireEvent.input(retryLimit, { target: { value: '8' } });
    await fireEvent.change(retryLimit);
    await waitFor(() => expect(backend.SetAbsentStationRetryLimit).toHaveBeenCalledWith(8));

    const initRetry = screen.getByLabelText('Adapter init retry');
    await fireEvent.input(initRetry, { target: { value: '5' } });
    await fireEvent.change(initRetry);
    await waitFor(() => expect(backend.SetBluetoothInitRetrySeconds).toHaveBeenCalledWith(5));
  });

  it('rolls recovery settings back when saving fails', async () => {
    backend.SetRecoveryRetryBaseSeconds.mockRejectedValue(new Error('above the retry maximum'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Recovery retry base');
    await fireEvent.input(input, { target: { value: '60' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetRecoveryRetryBaseSeconds).toHaveBeenCalledWith(60));
    await waitFor(() => expect(input).toHaveValue(30));
    expect(pushToast).toHaveBeenCalledWith('Recovery settings could not be saved: Error: above the retry maximum');
  });

  it('persists connection timing settings', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const finalWrite = await screen.findByLabelText('Sleep final write timeout');
    await fireEvent.input(finalWrite, { target: { value: '60' } });
    await fireEvent.change(finalWrite);
    await waitFor(() => expect(backend.SetSleepFinalWriteTimeoutSeconds).toHaveBeenCalledWith(60));

    const attempts = screen.getByLabelText('GATT discovery attempts');
    await fireEvent.input(attempts, { target: { value: '5' } });
    await fireEvent.change(attempts);
    await waitFor(() => expect(backend.SetDiscoveryAttempts).toHaveBeenCalledWith(5));

    const retryDelay = screen.getByLabelText('Discovery retry delay');
    await fireEvent.input(retryDelay, { target: { value: '1000' } });
    await fireEvent.change(retryDelay);
    await waitFor(() => expect(backend.SetDiscoveryRetryDelayMs).toHaveBeenCalledWith(1000));
  });

  it('persists the API listen address', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Listen address');
    await fireEvent.input(input, { target: { value: '127.0.0.1:8080' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetAPIListenAddress).toHaveBeenCalledWith('127.0.0.1:8080'));
  });

  it('rolls the API listen address back when saving fails', async () => {
    backend.SetAPIListenAddress.mockRejectedValue(new Error('invalid address'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Listen address');
    await fireEvent.input(input, { target: { value: '127.0.0.1:80' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetAPIListenAddress).toHaveBeenCalledWith('127.0.0.1:80'));
    await waitFor(() => expect(input).toHaveValue('127.0.0.1:7575'));
    expect(pushToast).toHaveBeenCalledWith('HTTP API settings could not be saved: Error: invalid address');
  });

  it('rolls power confirmation settings back when saving fails', async () => {
    backend.SetPowerConfirmAttemptsOn.mockRejectedValue(new Error('config locked'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    const input = await screen.findByLabelText('Power-on confirmation attempts');
    await fireEvent.input(input, { target: { value: '25' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetPowerConfirmAttemptsOn).toHaveBeenCalledWith(25));
    await waitFor(() => expect(input).toHaveValue(51));
    expect(pushToast).toHaveBeenCalledWith('Power confirmation settings could not be saved: Error: config locked');
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

  it('keeps a persisted value when its immediate UI follow-up fails', async () => {
    const onStatusPollIntervalChanged = vi.fn(() => {
      throw new Error('poller unavailable');
    });
    render(SettingsPanel, { props: { onClose: vi.fn(), onStatusPollIntervalChanged } });
    const input = await screen.findByLabelText('Status polling interval');
    await fireEvent.input(input, { target: { value: '45' } });
    await fireEvent.change(input);

    await waitFor(() => expect(backend.SetStatusPollIntervalSeconds).toHaveBeenCalledWith(45));
    await waitFor(() => expect(input).toHaveValue(45));
    expect(onStatusPollIntervalChanged).toHaveBeenCalledWith(45);
    expect(pushToast).toHaveBeenCalledWith(
      'Setting was saved, but the current view could not apply it immediately: Error: poller unavailable',
      'warning'
    );
  });

  it('refreshes station projection after projection-affecting settings are saved', async () => {
    const onStationProjectionChanged = vi.fn();
    render(SettingsPanel, { props: { onClose: vi.fn(), onStationProjectionChanged } });
    const input = await screen.findByLabelText('Absence miss threshold');
    await fireEvent.input(input, { target: { value: '4' } });
    await fireEvent.change(input);
    await waitFor(() => expect(backend.SetPresenceMissThreshold).toHaveBeenCalledWith(4));
    expect(onStationProjectionChanged).toHaveBeenCalledOnce();

    const freshness = screen.getByLabelText('Channel scan freshness');
    await fireEvent.input(freshness, { target: { value: '180' } });
    await fireEvent.change(freshness);
    await waitFor(() => expect(backend.SetChannelScanFreshnessSeconds).toHaveBeenCalledWith(180));
    expect(onStationProjectionChanged).toHaveBeenCalledTimes(2);
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
