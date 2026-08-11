import { describe, expect, it, vi } from 'vitest';
import { FleetState } from './fleet-state.svelte';
import { createStation } from '../../test/fixtures';

describe('FleetState merge', () => {
  it('dedupes duplicate new addresses instead of adding repeated cards', () => {
    const fleet = new FleetState();
    const dupFirst = createStation({ address: 'AA', name: 'dup-first' });
    const dupLast = createStation({ address: 'AA', name: 'dup-last' });
    const other = createStation({ address: 'BB', name: 'other' });

    fleet.merge([dupFirst, dupLast, other]);

    expect(fleet.stations).toHaveLength(2);
    expect(fleet.stations.map((station) => station.address)).toEqual(['AA', 'BB']);
    // The last occurrence wins for a duplicated new address.
    expect(fleet.stations.find((station) => station.address === 'AA')?.name).toBe('dup-last');
  });

  it('merges updates for existing stations without duplication', () => {
    const fleet = new FleetState();
    fleet.replace([createStation({ address: 'AA', name: 'first' })]);

    fleet.merge([
      createStation({ address: 'AA', name: 'updated' }),
      createStation({ address: 'AA', name: 'updated-again' }),
      createStation({ address: 'BB', name: 'new' })
    ]);

    expect(fleet.stations).toHaveLength(2);
    expect(fleet.stations.find((station) => station.address === 'AA')?.name).toBe('updated-again');
    expect(fleet.stations.map((station) => station.address)).toEqual(['AA', 'BB']);
  });
});

describe('FleetState channel memory', () => {
  it('retains a long-lived channel for a full TTL after the first wipe', () => {
    vi.useFakeTimers();
    const fleet = new FleetState();
    try {
      fleet.replace([createStation({ address: 'AA', channel: 3 })]);
      fleet.syncChannelMemory();

      // Keeping a valid channel for longer than the retention TTL must not
      // consume the window that is intended for a later transient wipe.
      vi.advanceTimersByTime(60_000);
      const wiped = createStation({ address: 'AA', channel: 0, channelFresh: false });
      fleet.replace([wiped]);
      fleet.syncChannelMemory();

      expect(fleet.displayChannel(wiped)).toBe(3);
      vi.advanceTimersByTime(44_999);
      expect(fleet.displayChannel(wiped)).toBe(3);
      vi.advanceTimersByTime(2);
      expect(fleet.displayChannel(wiped)).toBe(0);
    } finally {
      fleet.stopChannelMemoryExpiry();
      vi.useRealTimers();
    }
  });
});
