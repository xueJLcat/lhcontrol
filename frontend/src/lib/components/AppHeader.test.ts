import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import AppHeader from './AppHeader.svelte';

afterEach(cleanup);

function renderHeader(overrides: Record<string, unknown> = {}) {
  const onBulkPower = vi.fn();
  const onOpenSettings = vi.fn();
  render(AppHeader, {
    props: {
      scanning: false,
      isBulkLoading: false,
      scanLocked: false,
      bulkLocked: false,
      bulkTarget: null,
      canOn: true,
      canStandby: true,
      canSleep: true,
      allOn: false,
      allStandby: false,
      allSleep: false,
      onScan: vi.fn(),
      onStop: vi.fn(),
      onBulkPower,
      onOpenSettings,
      ...overrides
    }
  });
  return { onBulkPower, onOpenSettings };
}

describe('AppHeader bulk controls', () => {
  it('keeps all three targets enabled when a known station exists', () => {
    renderHeader();
    expect(screen.getByRole('button', { name: 'On' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Standby' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Sleep' })).toBeEnabled();
  });

  it('disables all targets only when there is no station or Bluetooth is busy', () => {
    renderHeader({ canOn: false, canStandby: false, canSleep: false });
    expect(screen.getByRole('button', { name: 'On' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Standby' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Sleep' })).toBeDisabled();

    cleanup();
    renderHeader({ bulkLocked: true });
    expect(screen.getByRole('button', { name: 'On' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Standby' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Sleep' })).toBeDisabled();
  });

  it('dispatches the selected bulk target', async () => {
    const { onBulkPower } = renderHeader();
    await fireEvent.click(screen.getByRole('button', { name: 'Standby' }));
    expect(onBulkPower).toHaveBeenCalledWith('standby');
  });

  it('opens the settings drawer from the gear button', async () => {
    const { onOpenSettings } = renderHeader();
    await fireEvent.click(screen.getByRole('button', { name: 'Open settings' }));
    expect(onOpenSettings).toHaveBeenCalledTimes(1);
  });

  it('shows scan progress for an external scan as well as a local scan', () => {
    renderHeader({ scanning: true, scanLocked: true });
    expect(screen.getByRole('button', { name: 'Stop' })).toBeEnabled();
    expect(document.querySelector('.scan-progress')).toBeInTheDocument();
  });
});
