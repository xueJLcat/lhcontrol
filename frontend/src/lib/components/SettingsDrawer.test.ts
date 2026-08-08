import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { autosleep, bluetooth } from '../../../wailsjs/go/models';
import SettingsDrawer from './SettingsDrawer.svelte';

afterEach(cleanup);

beforeEach(() => {
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
});

function adapter(deviceId: string, name: string): bluetooth.AdapterInfo {
  return bluetooth.AdapterInfo.createFrom({ deviceId, name });
}

function autoSleep(overrides: Record<string, unknown> = {}): autosleep.Settings {
  return autosleep.Settings.createFrom({
    enabled: false,
    target: 'steamvr',
    delaySeconds: 300,
    ...overrides
  });
}

function defaultProps(overrides: Record<string, unknown> = {}) {
  return {
    adapters: [adapter('USB\\VID-1', 'Intel Wireless Bluetooth'), adapter('USB\\VID-2', 'CSR8510 Dongle')],
    loading: false,
    loadError: null,
    autoSleep: autoSleep(),
    autoSleepBusy: false,
    scanOnStartup: true,
    scanOnStartupBusy: false,
    scanDurationSeconds: 5,
    scanDurationBusy: false,
    statusPollingEnabled: true,
    statusPollingEnabledBusy: false,
    bulkPowerTimeoutSeconds: 120,
    bulkPowerTimeoutBusy: false,
    statusPollIntervalSeconds: 15,
    statusPollIntervalBusy: false,
    stationOperationTimeoutSeconds: 30,
    stationOperationTimeoutBusy: false,
    powerConfirmAttemptsOn: 51,
    powerConfirmAttemptsOnBusy: false,
    powerConfirmAttemptsOff: 15,
    powerConfirmAttemptsOffBusy: false,
    powerConfirmPollIntervalMs: 200,
    powerConfirmPollIntervalBusy: false,
    bootFallbackSeconds: 8,
    bootFallbackBusy: false,
    sleepFinalWriteTimeoutSeconds: 30,
    sleepFinalWriteTimeoutBusy: false,
    sleepPrepareGapMs: 50,
    sleepPrepareGapBusy: false,
    discoveryAttempts: 3,
    discoveryAttemptsBusy: false,
    discoveryRetryDelayMs: 500,
    discoveryRetryDelayBusy: false,
    apiListenAddress: '127.0.0.1:7575',
    apiListenAddressBusy: false,
    powerWriteAttempts: 2,
    powerWriteAttemptsBusy: false,
    operationRetryDelayMs: 500,
    operationRetryDelayBusy: false,
    identifyAttempts: 2,
    identifyAttemptsBusy: false,
    confirmReconnectThreshold: 2,
    confirmReconnectThresholdBusy: false,
    confirmReconnectDelayMs: 250,
    confirmReconnectDelayBusy: false,
    channelConfirmAttempts: 5,
    channelConfirmAttemptsBusy: false,
    channelConfirmIntervalMs: 250,
    channelConfirmIntervalBusy: false,
    presenceMissThreshold: 2,
    presenceMissThresholdBusy: false,
    initialReadTimeoutSeconds: 30,
    initialReadTimeoutBusy: false,
    scanReadPhaseTimeoutSeconds: 45,
    scanReadPhaseTimeoutBusy: false,
    statusReadTimeoutSeconds: 20,
    statusReadTimeoutBusy: false,
    statusRefreshTimeoutSeconds: 30,
    statusRefreshTimeoutBusy: false,
    channelScanFreshnessSeconds: 120,
    channelScanFreshnessBusy: false,
    recoveryRetryBaseSeconds: 30,
    recoveryRetryBaseBusy: false,
    recoveryRetryMaxSeconds: 300,
    recoveryRetryMaxBusy: false,
    absentStationRetryLimit: 5,
    absentStationRetryLimitBusy: false,
    bluetoothInitRetrySeconds: 2,
    bluetoothInitRetryBusy: false,
    inactive: false,
    onClose: vi.fn(),
    onRefresh: vi.fn(),
    onAutoSleepChange: vi.fn(),
    onAutoSleepRetry: vi.fn(),
    onScanOnStartupChange: vi.fn(),
    onScanOnStartupRetry: vi.fn(),
    onScanDurationChange: vi.fn(),
    onScanDurationRetry: vi.fn(),
    onStatusPollingEnabledChange: vi.fn(),
    onStatusPollingEnabledRetry: vi.fn(),
    onBulkPowerTimeoutChange: vi.fn(),
    onBulkPowerTimeoutRetry: vi.fn(),
    onStatusPollIntervalChange: vi.fn(),
    onStatusPollIntervalRetry: vi.fn(),
    onStationOperationTimeoutChange: vi.fn(),
    onStationOperationTimeoutRetry: vi.fn(),
    onPowerConfirmAttemptsOnChange: vi.fn(),
    onPowerConfirmAttemptsOnRetry: vi.fn(),
    onPowerConfirmAttemptsOffChange: vi.fn(),
    onPowerConfirmAttemptsOffRetry: vi.fn(),
    onPowerConfirmPollIntervalChange: vi.fn(),
    onPowerConfirmPollIntervalRetry: vi.fn(),
    onBootFallbackChange: vi.fn(),
    onBootFallbackRetry: vi.fn(),
    onSleepFinalWriteTimeoutChange: vi.fn(),
    onSleepFinalWriteTimeoutRetry: vi.fn(),
    onSleepPrepareGapChange: vi.fn(),
    onSleepPrepareGapRetry: vi.fn(),
    onDiscoveryAttemptsChange: vi.fn(),
    onDiscoveryAttemptsRetry: vi.fn(),
    onDiscoveryRetryDelayChange: vi.fn(),
    onDiscoveryRetryDelayRetry: vi.fn(),
    onAPIListenAddressChange: vi.fn(),
    onAPIListenAddressRetry: vi.fn(),
    onPowerWriteAttemptsChange: vi.fn(),
    onPowerWriteAttemptsRetry: vi.fn(),
    onOperationRetryDelayChange: vi.fn(),
    onOperationRetryDelayRetry: vi.fn(),
    onIdentifyAttemptsChange: vi.fn(),
    onIdentifyAttemptsRetry: vi.fn(),
    onConfirmReconnectThresholdChange: vi.fn(),
    onConfirmReconnectThresholdRetry: vi.fn(),
    onConfirmReconnectDelayChange: vi.fn(),
    onConfirmReconnectDelayRetry: vi.fn(),
    onChannelConfirmAttemptsChange: vi.fn(),
    onChannelConfirmAttemptsRetry: vi.fn(),
    onChannelConfirmIntervalChange: vi.fn(),
    onChannelConfirmIntervalRetry: vi.fn(),
    onPresenceMissThresholdChange: vi.fn(),
    onPresenceMissThresholdRetry: vi.fn(),
    onInitialReadTimeoutChange: vi.fn(),
    onInitialReadTimeoutRetry: vi.fn(),
    onScanReadPhaseTimeoutChange: vi.fn(),
    onScanReadPhaseTimeoutRetry: vi.fn(),
    onStatusReadTimeoutChange: vi.fn(),
    onStatusReadTimeoutRetry: vi.fn(),
    onStatusRefreshTimeoutChange: vi.fn(),
    onStatusRefreshTimeoutRetry: vi.fn(),
    onChannelScanFreshnessChange: vi.fn(),
    onChannelScanFreshnessRetry: vi.fn(),
    onRecoveryRetryBaseChange: vi.fn(),
    onRecoveryRetryBaseRetry: vi.fn(),
    onRecoveryRetryMaxChange: vi.fn(),
    onRecoveryRetryMaxRetry: vi.fn(),
    onAbsentStationRetryLimitChange: vi.fn(),
    onAbsentStationRetryLimitRetry: vi.fn(),
    onBluetoothInitRetryChange: vi.fn(),
    onBluetoothInitRetryRetry: vi.fn(),
    ...overrides
  };
}

describe('SettingsDrawer', () => {
  it('orders user preferences before automation, safety, and diagnostics', () => {
    render(SettingsDrawer, { props: defaultProps() });
    expect(screen.getAllByRole('heading', { level: 4 }).map((heading) => heading.textContent?.trim())).toEqual([
      'Language',
      'Scanning and refresh',
      'Auto sleep',
      'Operation safety',
      'Power operation timing',
      'Connection timing',
      'Channel and presence',
      'HTTP API',
      'Read budgets',
      'Background recovery',
      'Bluetooth diagnostics'
    ]);
  });

  it('offers a system language preference separately from explicit languages', () => {
    render(SettingsDrawer, { props: defaultProps() });
    expect(screen.getByRole('radio', { name: 'Follow system' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'English' })).not.toBeChecked();
    expect(screen.getByRole('radio', { name: '简体中文' })).not.toBeChecked();
  });

  it('lists every detected adapter as read-only diagnostics', () => {
    render(SettingsDrawer, { props: defaultProps() });
    expect(screen.getByText('Intel Wireless Bluetooth')).toBeInTheDocument();
    expect(screen.getByText('CSR8510 Dongle')).toBeInTheDocument();
    expect(screen.getByText('USB\\VID-1')).toBeInTheDocument();
    expect(screen.getByText('USB\\VID-2')).toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: /Bluetooth/ })).not.toBeInTheDocument();
    expect(screen.getByText(/Windows controls which radio/)).toBeInTheDocument();
  });

  it('commits startup, scan-duration, and automatic-refresh preferences', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });

    await fireEvent.click(screen.getByRole('checkbox', { name: 'Scan when the application starts' }));
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Refresh station status automatically' }));
    const duration = screen.getByLabelText('Bluetooth scan duration') as HTMLInputElement;
    await fireEvent.input(duration, { target: { value: '45' } });
    await fireEvent.change(duration);

    expect(props.onScanOnStartupChange).toHaveBeenCalledWith(false);
    expect(props.onStatusPollingEnabledChange).toHaveBeenCalledWith(false);
    expect(props.onScanDurationChange).toHaveBeenCalledWith(30);
  });

  it('shows the loading state without an adapter list', () => {
    render(SettingsDrawer, { props: defaultProps({ loading: true, adapters: [] }) });
    expect(screen.getByText(/Detecting Bluetooth adapters/)).toBeInTheDocument();
    expect(screen.queryByText('Default')).not.toBeInTheDocument();
  });

  it('shows enumeration errors with a retry action', async () => {
    const props = defaultProps({ loadError: 'Bluetooth enumeration failed' });
    render(SettingsDrawer, { props });
    expect(screen.getByText('Bluetooth enumeration failed')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: /Retry/ }));
    expect(props.onRefresh).toHaveBeenCalledOnce();
  });

  it('uses modal semantics and becomes inert under a child modal', async () => {
    const view = render(SettingsDrawer, { props: defaultProps() });
    const drawer = screen.getByRole('dialog', { name: 'Settings' });
    expect(drawer).toHaveAttribute('aria-modal', 'true');

    await view.rerender(defaultProps({ inactive: true }));
    const hiddenDrawer = document.querySelector('.drawer');
    expect(hiddenDrawer).toHaveProperty('inert', true);
    expect(hiddenDrawer).toHaveAttribute('aria-hidden', 'true');
    expect(hiddenDrawer).not.toHaveAttribute('aria-modal');
  });

  it('clamps and commits the bulk operation timeout in seconds', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Bulk power timeout') as HTMLInputElement;
    expect(input.value).toBe('120');
    await fireEvent.input(input, { target: { value: '999' } });
    await fireEvent.change(input);
    expect(props.onBulkPowerTimeoutChange).toHaveBeenCalledWith(600);
    expect(input.value).toBe('600');
  });

  it('clamps and commits the status polling interval in seconds', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Status polling interval') as HTMLInputElement;
    expect(input.value).toBe('15');
    await fireEvent.input(input, { target: { value: '2' } });
    await fireEvent.change(input);
    expect(props.onStatusPollIntervalChange).toHaveBeenCalledWith(5);
    expect(input.value).toBe('5');
  });

  it('restores the current value when a numeric input is cleared', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const scenarios: Array<[string, number]> = [
      ['Bulk power timeout', 120],
      ['Status polling interval', 15],
      ['Bluetooth scan duration', 5],
      ['Station operation timeout', 30],
      ['Power-on confirmation attempts', 51],
      ['Sleep/standby confirmation attempts', 15],
      ['Confirmation read interval', 200],
      ['Boot fallback window', 8],
      ['Power write attempts', 2],
      ['Identify attempts', 2],
      ['Initial read timeout', 30],
      ['Recovery retry base', 30],
      ['Absent station retry limit', 5]
    ];
    for (const [label, current] of scenarios) {
      const input = screen.getByLabelText(label) as HTMLInputElement;
      await fireEvent.input(input, { target: { value: '' } });
      await fireEvent.change(input);
      expect(input.value).toBe(String(current));
    }
    expect(props.onBulkPowerTimeoutChange).not.toHaveBeenCalled();
    expect(props.onStatusPollIntervalChange).not.toHaveBeenCalled();
    expect(props.onScanDurationChange).not.toHaveBeenCalled();
    expect(props.onStationOperationTimeoutChange).not.toHaveBeenCalled();
    expect(props.onPowerConfirmAttemptsOnChange).not.toHaveBeenCalled();
    expect(props.onPowerConfirmAttemptsOffChange).not.toHaveBeenCalled();
    expect(props.onPowerConfirmPollIntervalChange).not.toHaveBeenCalled();
    expect(props.onBootFallbackChange).not.toHaveBeenCalled();
    expect(props.onPowerWriteAttemptsChange).not.toHaveBeenCalled();
    expect(props.onIdentifyAttemptsChange).not.toHaveBeenCalled();
    expect(props.onInitialReadTimeoutChange).not.toHaveBeenCalled();
    expect(props.onRecoveryRetryBaseChange).not.toHaveBeenCalled();
    expect(props.onAbsentStationRetryLimitChange).not.toHaveBeenCalled();
  });

  it('clamps and commits the station operation timeout in seconds', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Station operation timeout') as HTMLInputElement;
    expect(input.value).toBe('30');
    await fireEvent.input(input, { target: { value: '999' } });
    await fireEvent.change(input);
    expect(props.onStationOperationTimeoutChange).toHaveBeenCalledWith(120);
    expect(input.value).toBe('120');
  });

  it('rejects a station operation timeout below the minimum', async () => {
    const props = defaultProps({ stationOperationTimeoutSeconds: 60 });
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Station operation timeout') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: '5' } });
    await fireEvent.change(input);
    expect(props.onStationOperationTimeoutChange).toHaveBeenCalledWith(30);
  });

  it('clamps and commits the power confirmation timing inputs', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const attemptsOn = screen.getByLabelText('Power-on confirmation attempts') as HTMLInputElement;
    await fireEvent.input(attemptsOn, { target: { value: '999' } });
    await fireEvent.change(attemptsOn);
    expect(props.onPowerConfirmAttemptsOnChange).toHaveBeenCalledWith(200);

    const attemptsOff = screen.getByLabelText('Sleep/standby confirmation attempts') as HTMLInputElement;
    await fireEvent.input(attemptsOff, { target: { value: '1' } });
    await fireEvent.change(attemptsOff);
    expect(props.onPowerConfirmAttemptsOffChange).toHaveBeenCalledWith(5);

    const pollInterval = screen.getByLabelText('Confirmation read interval') as HTMLInputElement;
    await fireEvent.input(pollInterval, { target: { value: '9999' } });
    await fireEvent.change(pollInterval);
    expect(props.onPowerConfirmPollIntervalChange).toHaveBeenCalledWith(2000);

    const bootFallback = screen.getByLabelText('Boot fallback window') as HTMLInputElement;
    await fireEvent.input(bootFallback, { target: { value: '1' } });
    await fireEvent.change(bootFallback);
    expect(props.onBootFallbackChange).toHaveBeenCalledWith(2);
  });

  it('clamps and commits the connection timing inputs', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const finalWrite = screen.getByLabelText('Sleep final write timeout') as HTMLInputElement;
    await fireEvent.input(finalWrite, { target: { value: '999' } });
    await fireEvent.change(finalWrite);
    expect(props.onSleepFinalWriteTimeoutChange).toHaveBeenCalledWith(120);

    const prepareGap = screen.getByLabelText('Sleep prepare gap') as HTMLInputElement;
    await fireEvent.input(prepareGap, { target: { value: '0' } });
    await fireEvent.change(prepareGap);
    expect(props.onSleepPrepareGapChange).toHaveBeenCalledWith(0);

    const attempts = screen.getByLabelText('GATT discovery attempts') as HTMLInputElement;
    await fireEvent.input(attempts, { target: { value: '0' } });
    await fireEvent.change(attempts);
    expect(props.onDiscoveryAttemptsChange).toHaveBeenCalledWith(1);

    const retryDelay = screen.getByLabelText('Discovery retry delay') as HTMLInputElement;
    await fireEvent.input(retryDelay, { target: { value: '99999' } });
    await fireEvent.change(retryDelay);
    expect(props.onDiscoveryRetryDelayChange).toHaveBeenCalledWith(5000);
  });

  it('restores the cleared sleep prepare gap to its current value', async () => {
    const props = defaultProps({ sleepPrepareGapMs: 50 });
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Sleep prepare gap') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: '' } });
    await fireEvent.change(input);
    expect(input.value).toBe('50');
    expect(props.onSleepPrepareGapChange).not.toHaveBeenCalled();
  });

  it('commits a changed API listen address and restores a cleared one', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Listen address') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: '127.0.0.1:8080' } });
    await fireEvent.change(input);
    expect(props.onAPIListenAddressChange).toHaveBeenCalledWith('127.0.0.1:8080');

    await fireEvent.input(input, { target: { value: '   ' } });
    await fireEvent.change(input);
    expect(input.value).toBe('127.0.0.1:7575');
    expect(props.onAPIListenAddressChange).toHaveBeenCalledTimes(1);
  });

  it('clamps and commits the advanced timing inputs', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    const writeAttempts = screen.getByLabelText('Power write attempts') as HTMLInputElement;
    await fireEvent.input(writeAttempts, { target: { value: '99' } });
    await fireEvent.change(writeAttempts);
    expect(props.onPowerWriteAttemptsChange).toHaveBeenCalledWith(5);

    const retryDelay = screen.getByLabelText('Power retry delay') as HTMLInputElement;
    await fireEvent.input(retryDelay, { target: { value: '99999' } });
    await fireEvent.change(retryDelay);
    expect(props.onOperationRetryDelayChange).toHaveBeenCalledWith(5000);

    const presence = screen.getByLabelText('Absence miss threshold') as HTMLInputElement;
    await fireEvent.input(presence, { target: { value: '99' } });
    await fireEvent.change(presence);
    expect(props.onPresenceMissThresholdChange).toHaveBeenCalledWith(10);

    const recoveryMax = screen.getByLabelText('Recovery retry maximum') as HTMLInputElement;
    await fireEvent.input(recoveryMax, { target: { value: '0' } });
    await fireEvent.change(recoveryMax);
    expect(props.onRecoveryRetryMaxChange).toHaveBeenCalledWith(60);

    const channelFreshness = screen.getByLabelText('Channel scan freshness') as HTMLInputElement;
    await fireEvent.input(channelFreshness, { target: { value: '9999' } });
    await fireEvent.change(channelFreshness);
    expect(props.onChannelScanFreshnessChange).toHaveBeenCalledWith(600);
  });

  it('restores the current delay when the delay input is cleared', async () => {
    const props = defaultProps({ autoSleep: autoSleep({ enabled: true, delaySeconds: 720 }) });
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Wait before sleeping') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: '   ' } });
    await fireEvent.change(input);
    expect(input.value).toBe('12');
    expect(props.onAutoSleepChange).not.toHaveBeenCalled();
  });

  it('does not round-trip a non-whole-minute delay when the input is cleared', async () => {
    // A hand-edited config can store a delay that is not a whole number of
    // minutes (90s). Clearing the field must restore the displayed value
    // without committing it, which would otherwise rewrite 90s as 120s.
    const props = defaultProps({ autoSleep: autoSleep({ enabled: true, delaySeconds: 90 }) });
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Wait before sleeping') as HTMLInputElement;
    expect(input.value).toBe('2');
    await fireEvent.input(input, { target: { value: '' } });
    await fireEvent.change(input);
    expect(input.value).toBe('2');
    expect(props.onAutoSleepChange).not.toHaveBeenCalled();
  });
});

describe('SettingsDrawer auto sleep', () => {
  it('shows the enable switch but hides options while disabled', () => {
    render(SettingsDrawer, { props: defaultProps() });
    expect(screen.getByRole('checkbox', { name: 'Enable auto sleep' })).not.toBeChecked();
    expect(screen.queryByRole('radiogroup', { name: 'Auto sleep trigger' })).not.toBeInTheDocument();
  });

  it('shows a loading placeholder when settings have not loaded', () => {
    render(SettingsDrawer, { props: defaultProps({ autoSleep: null }) });
    expect(screen.getByText(/Loading auto sleep settings/)).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Enable auto sleep' })).not.toBeInTheDocument();
  });

  it('shows load failures with a retry action instead of a permanent spinner', async () => {
    const props = defaultProps({ autoSleep: null, autoSleepError: 'settings unavailable' });
    render(SettingsDrawer, { props });
    expect(screen.queryByText(/Loading auto sleep settings/)).not.toBeInTheDocument();
    expect(screen.getByText('settings unavailable')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: /Retry/ }));
    expect(props.onAutoSleepRetry).toHaveBeenCalledOnce();
  });

  it('toggles the feature on and off', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    await fireEvent.click(screen.getByRole('checkbox', { name: 'Enable auto sleep' }));
    expect(props.onAutoSleepChange).toHaveBeenCalledTimes(1);
    const next = props.onAutoSleepChange.mock.calls[0][0] as autosleep.Settings;
    expect(next.enabled).toBe(true);
    expect(next.target).toBe('steamvr');
    expect(next.delaySeconds).toBe(300);
  });

  it('lets the user pick the watched process', async () => {
    const props = defaultProps({ autoSleep: autoSleep({ enabled: true }) });
    render(SettingsDrawer, { props });
    const radios = screen.getAllByRole('radio').filter((radio) => (radio as HTMLInputElement).name === 'auto-sleep-target');
    expect(radios).toHaveLength(2);
    const steam = radios.find((radio) => (radio as HTMLInputElement).value === 'steam');
    expect(steam).not.toBeChecked();
    await fireEvent.click(steam as HTMLElement);
    expect(props.onAutoSleepChange).toHaveBeenCalledTimes(1);
    const next = props.onAutoSleepChange.mock.calls[0][0] as autosleep.Settings;
    expect(next.enabled).toBe(true);
    expect(next.target).toBe('steam');
  });

  it('commits the delay in minutes when the input changes', async () => {
    const props = defaultProps({ autoSleep: autoSleep({ enabled: true }) });
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Wait before sleeping') as HTMLInputElement;
    expect(input.value).toBe('5');
    await fireEvent.input(input, { target: { value: '12' } });
    await fireEvent.change(input);
    expect(props.onAutoSleepChange).toHaveBeenCalledTimes(1);
    const next = props.onAutoSleepChange.mock.calls[0][0] as autosleep.Settings;
    expect(next.delaySeconds).toBe(720);
  });

  it('clamps out-of-range delays instead of saving them', async () => {
    const props = defaultProps({ autoSleep: autoSleep({ enabled: true }) });
    render(SettingsDrawer, { props });
    const input = screen.getByLabelText('Wait before sleeping') as HTMLInputElement;
    await fireEvent.input(input, { target: { value: '999' } });
    await fireEvent.change(input);
    const next = props.onAutoSleepChange.mock.calls[0][0] as autosleep.Settings;
    expect(next.delaySeconds).toBe(7200);
    expect(input.value).toBe('120');
  });

  it('locks the auto sleep controls while a change is being saved', () => {
    render(SettingsDrawer, { props: defaultProps({ autoSleep: autoSleep({ enabled: true }), autoSleepBusy: true }) });
    expect(screen.getByRole('checkbox', { name: 'Enable auto sleep' })).toBeDisabled();
    const targetRadios = screen.getAllByRole('radio')
      .filter((radio) => (radio as HTMLInputElement).name === 'auto-sleep-target');
    expect(targetRadios).toHaveLength(2);
    for (const radio of targetRadios) {
      expect(radio).toBeDisabled();
    }
    expect(screen.getByLabelText('Wait before sleeping')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Refresh adapters' })).toBeEnabled();
  });
});
