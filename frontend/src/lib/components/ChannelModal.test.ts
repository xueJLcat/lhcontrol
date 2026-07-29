import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
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
  return {
    name: 'LHB-A',
    originalName: 'LHB-A',
    address: '11:22:33:44:55:66',
    powerState: 1,
    powerStateName: 'on',
    powerStateConfirmed: true,
    rawPowerState: 0x0b,
    channel: 3,
    channelConflict: false,
    isPresent: true,
    seenInLatestScan: true,
    scanFresh: true,
    missedScans: 0,
    lastSeenAt: '',
    lastReadAt: '',
    lastPowerReadAt: '',
    lastChannelReadAt: '',
    metadataReadAt: '',
    lastError: '',
    statusFresh: true,
    powerFresh: true,
    channelFresh: true,
    metadataFresh: false,
    connectionState: 'connected',
    capabilitiesKnown: true,
    capabilities: {
      powerRead: true,
      powerWrite: true,
      powerNotify: false,
      standby: true,
      channelRead: true,
      channelWrite: true,
      channelNotify: false,
      identify: true,
      deviceInformation: false
    },
    metadata: {
      manufacturer: '',
      model: '',
      serialNumber: '',
      hardwareRevision: '',
      firmwareRevision: ''
    },
    ...overrides
  } as StationInfo;
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
    const cell = screen.getByRole('button', { name: '4' });
    // Chromium suppresses hover events and title tooltips on truly disabled
    // buttons, so the cell must stay enabled and use aria-disabled instead.
    expect(cell).not.toBeDisabled();
    expect(cell).toHaveAttribute('aria-disabled', 'true');
    expect(cell).toHaveAttribute('title', 'Occupied by LHB-B');
  });

  it('lists every station occupying the same channel', () => {
    renderModal({ occupiedChannels: new Map([[4, ['LHB-B', 'LHB-C']]]) });
    expect(screen.getByRole('button', { name: '4' }))
      .toHaveAttribute('title', 'Occupied by LHB-B, LHB-C');
  });

  it('does not select an occupied channel when clicked', async () => {
    renderModal();
    const cell = screen.getByRole('button', { name: '4' });
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
});
