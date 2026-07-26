import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
import ChannelMap from './ChannelMap.svelte';

afterEach(cleanup);

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
    metadataLoaded: false,
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

describe('ChannelMap', () => {
  it('renders free channels as disabled cells and occupied channels as actionable', () => {
    render(ChannelMap, { props: { stations: [station()], onSelect: vi.fn() } });
    expect(screen.getByRole('button', { name: 'CH 1 — free' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'CH 3 — LHB-A · on' })).toBeEnabled();
  });

  it('opens the occupying station when a channel cell is clicked', async () => {
    const onSelect = vi.fn();
    render(ChannelMap, { props: { stations: [station()], onSelect } });
    await fireEvent.click(screen.getByRole('button', { name: 'CH 3 — LHB-A · on' }));
    expect(onSelect).toHaveBeenCalledWith('11:22:33:44:55:66');
  });

  it('lists every occupant of a conflicting channel', () => {
    render(ChannelMap, {
      props: {
        stations: [
          station({ channelConflict: true }),
          station({ name: 'LHB-B', address: 'AA:BB:CC:DD:EE:FF', channelConflict: true })
        ],
        onSelect: vi.fn()
      }
    });
    const cell = screen.getByRole('button', { name: 'CH 3 — LHB-A · on, LHB-B · on' });
    expect(cell).toHaveClass('conflict');
  });

  it('ignores stations without a known channel', () => {
    render(ChannelMap, { props: { stations: [station({ channel: 0 })], onSelect: vi.fn() } });
    expect(screen.getByRole('button', { name: 'CH 3 — free' })).toBeDisabled();
  });

  it('keeps stale and absent channel assignments visible as last known', () => {
    render(ChannelMap, {
      props: {
        stations: [
          station({ channel: 3, channelFresh: false }),
          station({ channel: 4, isPresent: false })
        ],
        onSelect: vi.fn()
      }
    });
    const stale = screen.getByRole('button', { name: 'CH 3 — LHB-A · on · last known' });
    const absent = screen.getByRole('button', { name: 'CH 4 — LHB-A · on · last known' });
    expect(stale).toBeEnabled();
    expect(stale).toHaveClass('stale');
    expect(absent).toBeEnabled();
    expect(absent).toHaveClass('stale');
  });
});
