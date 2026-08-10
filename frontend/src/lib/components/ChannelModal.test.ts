import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
import { createOnStation } from '../../test/fixtures';
import ChannelModal from './ChannelModal.svelte';

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

function station(overrides: Partial<StationInfo> = {}): StationInfo {
  return createOnStation({ name: 'LHB-A', originalName: 'LHB-A', ...overrides });
}

function renderModal(overrides: Record<string, unknown> = {}) {
  const onSave = vi.fn();
  render(ChannelModal, {
    props: {
      station: station(),
      occupiedChannels: new Map([[4, ['LHB-B']]]),
      hasUnknownVisibleChannel: false,
      error: '',
      warning: false,
      busy: false,
      locked: false,
      onClose: vi.fn(),
      onSave,
      onIdentify: vi.fn(),
      ...overrides
    }
  });
  return onSave;
}

describe('ChannelModal channel grid', () => {
  it('keeps occupied channels focusable with their tooltip instead of disabling them', () => {
    renderModal();
    const cell = screen.getByRole('button', { name: 'Channel 4, occupied by LHB-B' });
    // Chromium suppresses hover events and title tooltips on truly disabled
    // buttons, so the cell must stay enabled and use aria-disabled instead.
    expect(cell).not.toBeDisabled();
    expect(cell).toHaveAttribute('aria-disabled', 'true');
    expect(cell).toHaveAttribute('title', 'Occupied by LHB-B');
  });

  it('lists every station occupying the same channel', () => {
    renderModal({ occupiedChannels: new Map([[4, ['LHB-B', 'LHB-C']]]) });
    expect(screen.getByRole('button', { name: 'Channel 4, occupied by LHB-B, LHB-C' }))
      .toHaveAttribute('title', 'Occupied by LHB-B, LHB-C');
  });

  it('does not select an occupied channel when clicked', async () => {
    renderModal();
    const cell = screen.getByRole('button', { name: 'Channel 4, occupied by LHB-B' });
    await fireEvent.click(cell);
    expect(cell).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeDisabled();
  });

  it('selects a free channel and saves it', async () => {
    const onSave = renderModal();
    const cell = screen.getByRole('button', { name: '5' });
    await fireEvent.click(cell);
    expect(cell).toHaveAttribute('aria-pressed', 'true');
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm change' }));
    expect(onSave).toHaveBeenCalledWith(5, false);
  });

  it('keeps confirm disabled while the target equals the current channel', () => {
    renderModal();
    expect(screen.getByRole('button', { name: '3' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeDisabled();
  });

  it('selects the first free channel when the current channel conflicts', () => {
    renderModal({
      station: station({ channelConflict: true }),
      occupiedChannels: new Map([[3, ['LHB-B']], [4, ['LHB-C']]])
    });

    expect(screen.getByRole('button', { name: '1' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeEnabled();
  });

  it('allows a stale current channel to be submitted for confirmation', async () => {
    const onSave = renderModal({ station: station({ channelFresh: false }) });
    const confirm = screen.getByRole('button', { name: 'Confirm change' });
    expect(confirm).toBeEnabled();
    await fireEvent.click(confirm);
    expect(onSave).toHaveBeenCalledWith(3, false);
  });

  it('blocks submission if the station starts booting while the modal is open', async () => {
    const onSave = vi.fn();
    const baseProps = {
      occupiedChannels: new Map<number, string[]>(),
      hasUnknownVisibleChannel: false,
      error: '',
      warning: false,
      busy: false,
      locked: false,
      onClose: vi.fn(),
      onSave,
      onIdentify: vi.fn()
    };
    const view = render(ChannelModal, {
      props: { ...baseProps, station: station() }
    });
    await fireEvent.click(screen.getByRole('button', { name: '5' }));
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeEnabled();

    await view.rerender({
      ...baseProps,
      station: station({ powerState: 3, powerStateName: 'booting', powerFresh: true })
    });
    const confirm = screen.getByRole('button', { name: 'Confirm change' });
    expect(confirm).toBeDisabled();
    expect(screen.getByText(/Wait for the station to finish booting/)).toBeInTheDocument();
    await fireEvent.click(confirm);
    expect(onSave).not.toHaveBeenCalled();

    await view.rerender({ ...baseProps, station: station() });
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeEnabled();
  });

  it('keeps the default selection a no-op when the live channel changes while open', async () => {
    const onSave = vi.fn();
    const baseProps = {
      occupiedChannels: new Map<number, string[]>(),
      hasUnknownVisibleChannel: false,
      error: '',
      warning: false,
      busy: false,
      locked: false,
      onClose: vi.fn(),
      onSave,
      onIdentify: vi.fn()
    };
    const view = render(ChannelModal, {
      props: { ...baseProps, station: station({ channel: 3, channelFresh: true }) }
    });
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeDisabled();

    // A background refresh moves the live channel; the untouched default must
    // follow it so Confirm stays a no-op instead of reverting the station.
    await view.rerender({ ...baseProps, station: station({ channel: 5, channelFresh: true }) });
    expect(screen.getByRole('button', { name: '5' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeDisabled();

    // An explicit user selection survives later live updates.
    await fireEvent.click(screen.getByRole('button', { name: '7' }));
    await view.rerender({ ...baseProps, station: station({ channel: 6, channelFresh: true }) });
    expect(screen.getByRole('button', { name: '7' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Confirm change' })).toBeEnabled();
    await fireEvent.click(screen.getByRole('button', { name: 'Confirm change' }));
    expect(onSave).toHaveBeenCalledWith(7, false);
  });

  it('describes the busy state by its actual operation', () => {
    cleanup();
    renderModal({ busy: true, saving: true });
    expect(screen.getByText('Writing channel and verifying the readback...')).toBeInTheDocument();
    cleanup();
    renderModal({ busy: true, saving: false });
    expect(screen.getByText('Bluetooth operation in progress')).toBeInTheDocument();
    expect(screen.queryByText('Writing channel and verifying the readback...')).not.toBeInTheDocument();
  });
});
