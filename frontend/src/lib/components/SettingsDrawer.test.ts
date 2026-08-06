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
    selectedDeviceId: '',
    busy: false,
    autoSleep: autoSleep(),
    autoSleepBusy: false,
    inactive: false,
    onClose: vi.fn(),
    onRefresh: vi.fn(),
    onSelect: vi.fn(),
    onAutoSleepChange: vi.fn(),
    ...overrides
  };
}

describe('SettingsDrawer', () => {
  it('offers the default option plus every detected adapter', () => {
    render(SettingsDrawer, { props: defaultProps() });
    const radios = screen.getAllByRole('radio');
    expect(radios).toHaveLength(3);
    expect(screen.getByText('Default')).toBeInTheDocument();
    expect(screen.getByText('Intel Wireless Bluetooth')).toBeInTheDocument();
    expect(screen.getByText('CSR8510 Dongle')).toBeInTheDocument();
  });

  it('marks the persisted adapter as selected', () => {
    render(SettingsDrawer, { props: defaultProps({ selectedDeviceId: 'USB\\VID-2' }) });
    const selected = screen.getAllByRole('radio').find((radio) => (radio as HTMLInputElement).checked);
    expect(selected).toBeDefined();
    expect((selected as HTMLInputElement).value).toBe('USB\\VID-2');
  });

  it('dispatches selection changes', async () => {
    const props = defaultProps();
    render(SettingsDrawer, { props });
    await fireEvent.click(screen.getByText('CSR8510 Dongle'));
    expect(props.onSelect).toHaveBeenCalledWith('USB\\VID-2');
  });

  it('clears the preference through the default option', async () => {
    const props = defaultProps({ selectedDeviceId: 'USB\\VID-1' });
    render(SettingsDrawer, { props });
    await fireEvent.click(screen.getByText('Default'));
    expect(props.onSelect).toHaveBeenCalledWith('');
  });

  it('keeps a missing persisted adapter visible with a warning', () => {
    render(SettingsDrawer, { props: defaultProps({ selectedDeviceId: 'USB\\GONE' }) });
    expect(screen.getByText(/Not currently detected/)).toBeInTheDocument();
    expect(screen.getByText('USB\\GONE')).toBeInTheDocument();
    const selected = screen.getAllByRole('radio').find((radio) => (radio as HTMLInputElement).checked);
    expect((selected as HTMLInputElement).value).toBe('USB\\GONE');
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

  it('locks the choices while a selection is being saved', () => {
    render(SettingsDrawer, { props: defaultProps({ busy: true }) });
    for (const radio of screen.getAllByRole('radio')) {
      expect(radio).toBeDisabled();
    }
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
    // The adapter picker above is independent and stays available.
    expect(screen.getByText('Default').closest('label')?.querySelector('input')).toBeEnabled();
  });
});
