import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
import DetailsDrawer from './DetailsDrawer.svelte';

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

function station(): StationInfo {
  return {
    name: 'LHB-TEST',
    originalName: 'LHB-TEST',
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
    }
  } as StationInfo;
}

describe('DetailsDrawer actions', () => {
  it('keeps all supported actions in one non-wrapping row', () => {
    render(DetailsDrawer, {
      props: {
        station: station(),
        busy: false,
        locked: false,
        inactive: false,
        onClose: vi.fn(),
        onRefresh: vi.fn(),
        onIdentify: vi.fn(),
        onOpenChannelEditor: vi.fn()
      }
    });

    const refresh = screen.getByRole('button', { name: /Refresh capabilities/i });
    const identify = screen.getByRole('button', { name: /Identify/i });
    const channel = screen.getByRole('button', { name: /Change Channel/i });
    expect(refresh.parentElement).toBe(identify.parentElement);
    expect(refresh.parentElement).toBe(channel.parentElement);
    expect(refresh.parentElement).toHaveClass('drawer-actions');
  });

  it('locks every action and dispatches each callback when unlocked', async () => {
    const onRefresh = vi.fn();
    const onIdentify = vi.fn();
    const onOpenChannelEditor = vi.fn();
    const view = render(DetailsDrawer, {
      props: {
        station: station(),
        busy: false,
        locked: true,
        inactive: false,
        onClose: vi.fn(),
        onRefresh,
        onIdentify,
        onOpenChannelEditor
      }
    });
    for (const name of [/Refresh capabilities/i, /Identify/i, /Change Channel/i]) {
      expect(screen.getByRole('button', { name })).toBeDisabled();
    }

    await view.rerender({
      station: station(),
      busy: false,
      locked: false,
      inactive: false,
      onClose: vi.fn(),
      onRefresh,
      onIdentify,
      onOpenChannelEditor
    });
    await fireEvent.click(screen.getByRole('button', { name: /Refresh capabilities/i }));
    await fireEvent.click(screen.getByRole('button', { name: /Identify/i }));
    await fireEvent.click(screen.getByRole('button', { name: /Change Channel/i }));
    expect(onRefresh).toHaveBeenCalledOnce();
    expect(onIdentify).toHaveBeenCalledOnce();
    expect(onOpenChannelEditor).toHaveBeenCalledOnce();
  });

  it('keeps recovery actions available when cached capabilities are missing', () => {
    const unknownCapabilities = station();
    unknownCapabilities.capabilitiesKnown = true;
    unknownCapabilities.capabilities.identify = false;
    unknownCapabilities.capabilities.channelRead = false;
    unknownCapabilities.capabilities.channelWrite = false;

    render(DetailsDrawer, {
      props: {
        station: unknownCapabilities,
        busy: false,
        locked: false,
        inactive: false,
        onClose: vi.fn(),
        onRefresh: vi.fn(),
        onIdentify: vi.fn(),
        onOpenChannelEditor: vi.fn()
      }
    });

    expect(screen.getByRole('button', { name: /Identify/i })).toBeEnabled();
    expect(screen.getByRole('button', { name: /Change Channel/i })).toBeEnabled();
  });

  it('distinguishes stale cached metadata from unavailable metadata', async () => {
    const stale = station();
    stale.metadataReadAt = '2026-07-20T10:00:00Z';
    stale.metadata.firmwareRevision = '1.2.3';
    const view = render(DetailsDrawer, {
      props: {
        station: stale, busy: false, locked: false, inactive: false,
        onClose: vi.fn(), onRefresh: vi.fn(), onIdentify: vi.fn(), onOpenChannelEditor: vi.fn()
      }
    });
    expect(screen.getByText(/cached, stale/)).toBeInTheDocument();

    await view.rerender({
      station: station(), busy: false, locked: false, inactive: false,
      onClose: vi.fn(), onRefresh: vi.fn(), onIdentify: vi.fn(), onOpenChannelEditor: vi.fn()
    });
    expect(screen.getByText(/unavailable · never/)).toBeInTheDocument();
  });

  it('uses modal semantics and becomes inert under a child modal', async () => {
    const view = render(DetailsDrawer, {
      props: {
        station: station(), busy: false, locked: false, inactive: false,
        onClose: vi.fn(), onRefresh: vi.fn(), onIdentify: vi.fn(), onOpenChannelEditor: vi.fn()
      }
    });
    const drawer = screen.getByRole('dialog', { name: 'Station details' });
    expect(drawer).toHaveAttribute('aria-modal', 'true');

    await view.rerender({
      station: station(), busy: false, locked: false, inactive: true,
      onClose: vi.fn(), onRefresh: vi.fn(), onIdentify: vi.fn(), onOpenChannelEditor: vi.fn()
    });
    const hiddenDrawer = document.querySelector('.drawer');
    expect(hiddenDrawer).toHaveProperty('inert', true);
    expect(hiddenDrawer).toHaveAttribute('aria-hidden', 'true');
    expect(hiddenDrawer).not.toHaveAttribute('aria-modal');
  });
});
