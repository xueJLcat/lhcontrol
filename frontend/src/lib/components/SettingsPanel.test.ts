import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  GetAutoSleepSettings: vi.fn(),
  GetBluetoothAdapter: vi.fn(),
  ListBluetoothAdapters: vi.fn(),
  SetAutoSleepSettings: vi.fn(),
  SetBluetoothAdapter: vi.fn()
}));

vi.mock('../backend', () => backend);
vi.mock('../toast', async (importOriginal) => {
  const original = await importOriginal<typeof import('../toast')>();
  return { ...original, pushToast: vi.fn() };
});

import SettingsPanel from './SettingsPanel.svelte';
import { pushToast } from '../toast';

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
  backend.GetBluetoothAdapter.mockResolvedValue('');
  backend.SetBluetoothAdapter.mockResolvedValue(undefined);
  backend.GetAutoSleepSettings.mockResolvedValue({ enabled: false, target: 'steamvr', delaySeconds: 300 });
  backend.SetAutoSleepSettings.mockResolvedValue(undefined);
});

describe('SettingsPanel', () => {
  it('loads adapter and auto-sleep settings on mount', async () => {
    backend.GetBluetoothAdapter.mockResolvedValue('BT-1');
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    expect(await screen.findByRole('dialog', { name: 'Settings' })).toBeInTheDocument();
    expect(backend.ListBluetoothAdapters).toHaveBeenCalledOnce();
    expect(backend.GetBluetoothAdapter).toHaveBeenCalledOnce();
    expect(backend.GetAutoSleepSettings).toHaveBeenCalledOnce();
    expect(await screen.findByText('Intel Wireless Bluetooth')).toBeInTheDocument();
    const selected = screen.getAllByRole('radio').find((radio) => (radio as HTMLInputElement).checked);
    expect((selected as HTMLInputElement).value).toBe('BT-1');
    expect(screen.getByRole('checkbox', { name: 'Enable auto sleep' })).not.toBeChecked();
  });

  it('saves a new adapter preference and reports the result', async () => {
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(await screen.findByText('Intel Wireless Bluetooth'));
    await waitFor(() => expect(backend.SetBluetoothAdapter).toHaveBeenCalledWith('BT-1'));
    expect(pushToast).toHaveBeenCalledWith('Bluetooth adapter preference saved.', 'success');
  });

  it('rolls the adapter selection back when saving fails', async () => {
    backend.GetBluetoothAdapter.mockResolvedValue('BT-1');
    backend.SetBluetoothAdapter.mockRejectedValue(new Error('config locked'));
    render(SettingsPanel, { props: { onClose: vi.fn() } });
    await screen.findByRole('dialog', { name: 'Settings' });
    await fireEvent.click(await screen.findByText('Default'));
    await waitFor(() => expect(backend.SetBluetoothAdapter).toHaveBeenCalledWith(''));
    expect(pushToast).toHaveBeenCalledWith('Bluetooth adapter preference could not be saved: Error: config locked');
    const selected = screen.getAllByRole('radio').find((radio) => (radio as HTMLInputElement).checked);
    expect((selected as HTMLInputElement).value).toBe('BT-1');
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
});
