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
    await waitFor(() => expect(within(channelDialog).getByRole('button', { name: 'Close channel editor' })).toBeDisabled());
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
    await vi.waitFor(() => expect(screen.queryByText('Switching to On…')).not.toBeInTheDocument());
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
    await fireEvent.click(within(dialog).getByRole('button', { name: 'Close channel editor' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Close station details' }));

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

});
