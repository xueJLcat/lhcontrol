import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
import { createOnStation } from '../../test/fixtures';
import ChannelMap from './ChannelMap.svelte';

afterEach(cleanup);

function station(overrides: Partial<StationInfo> = {}): StationInfo {
  return createOnStation({ name: 'LHB-A', originalName: 'LHB-A', ...overrides });
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

  it('shows stale and absent channel assignments as last-known without a hard conflict', () => {
    render(ChannelMap, {
      props: {
        stations: [
          station({ channel: 3, channelFresh: false }),
          station({ channel: 4, isPresent: false })
        ],
        onSelect: vi.fn()
      }
    });
    const stale = screen.getByRole('button', { name: 'CH 3 — LHB-A · on · last-known' });
    const absent = screen.getByRole('button', { name: 'CH 4 — LHB-A · on · last-known' });
    expect(stale).toBeEnabled();
    expect(stale).toHaveClass('stale');
    expect(stale).not.toHaveClass('conflict');
    expect(absent).toBeEnabled();
    expect(absent).toHaveClass('stale');
    expect(absent).not.toHaveClass('conflict');
  });

  it('does not count a last-known occupant as a hard conflict with a current occupant', () => {
    render(ChannelMap, {
      props: {
        stations: [
          station(),
          station({ name: 'LHB-B', address: 'BB', channelFresh: false })
        ],
        onSelect: vi.fn()
      }
    });
    expect(screen.getByRole('button', {
      name: 'CH 3 — LHB-A · on, LHB-B · on · last-known'
    })).not.toHaveClass('conflict');
  });

  it('groups stations by the injected channel snapshot so wiped channels stay stale', () => {
    const wiped = station({ channel: 0, channelFresh: false });
    render(ChannelMap, {
      props: {
        stations: [wiped],
        onSelect: vi.fn(),
        channelDisplayByAddress: new Map([[wiped.address, 3]])
      }
    });
    // Without the snapshot the cell would drop to free/disabled; with it the
    // occupant renders as last-known (stale) instead.
    const cell = screen.getByRole('button', { name: 'CH 3 — LHB-A · on · last-known' });
    expect(cell).toBeEnabled();
    expect(cell).toHaveClass('stale');
    expect(cell).not.toHaveClass('conflict');
  });

  it('highlights the channel cell of the selected station', () => {
    render(ChannelMap, {
      props: {
        stations: [station(), station({ name: 'LHB-B', address: 'BB', channel: 5 })],
        onSelect: vi.fn(),
        selectedAddress: '11:22:33:44:55:66'
      }
    });
    expect(screen.getByRole('button', { name: 'CH 3 — LHB-A · on' })).toHaveClass('selected');
    expect(screen.getByRole('button', { name: 'CH 5 — LHB-B · on' })).not.toHaveClass('selected');
  });

  it('does not highlight anything without a selection', () => {
    render(ChannelMap, { props: { stations: [station()], onSelect: vi.fn() } });
    expect(screen.getByRole('button', { name: 'CH 3 — LHB-A · on' })).not.toHaveClass('selected');
  });
});
