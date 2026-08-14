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

  it('ignores snapshots without an address instead of adding a ghost card', () => {
    const fleet = new FleetState();
    fleet.replace([createStation({ address: 'AA', name: 'known' })]);

    fleet.merge([createStation({ address: '', name: 'ghost' }), createStation({ address: 'BB', name: 'new' })]);

    expect(fleet.stations.map((station) => station.address)).toEqual(['AA', 'BB']);
    expect(fleet.visibleCount).toBe(2);
    expect(fleet.fleetUnverified + fleet.fleetOn + fleet.fleetStandby + fleet.fleetSleep).toBe(2);
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

describe('FleetState channel conflict risk', () => {
  it('treats a display-fresh but operationally stale peer channel as unknown rather than occupied', () => {
    const fleet = new FleetState();
    fleet.replace([
      createStation({ address: 'AA', channel: 3 }),
      createStation({ address: 'BB', channel: 4, channelFresh: true, channelOperationallyFresh: false })
    ]);

    expect(fleet.hasUnknownVisibleChannelExcluding('AA')).toBe(true);
    expect(fleet.occupiedChannelsExcluding('AA').has(4)).toBe(false);
  });

  it('expires operation freshness between slow backend polls without aging the display', async () => {
    vi.useFakeTimers();
    const fleet = new FleetState();
    try {
      fleet.replace([
        createStation({ address: 'AA', channel: 3 }),
        createStation({ address: 'BB', channel: 4 })
      ]);

      expect(fleet.actionableSleep).toHaveLength(0);
      expect(fleet.hasUnknownVisibleChannelExcluding('AA')).toBe(false);

      await vi.advanceTimersByTimeAsync(45_001);

      expect(fleet.stations.every((station) => station.powerFresh && station.channelFresh)).toBe(true);
      expect(fleet.stations.every((station) =>
        !station.powerOperationallyFresh && !station.channelOperationallyFresh
      )).toBe(true);
      expect(fleet.actionableSleep).toHaveLength(2);
      expect(fleet.hasUnknownVisibleChannelExcluding('AA')).toBe(true);
    } finally {
      fleet.stopChannelMemoryExpiry();
      vi.useRealTimers();
    }
  });

  it('rearms the expiry timer when a newer read extends the operation window', async () => {
    vi.useFakeTimers();
    const fleet = new FleetState();
    try {
      const initial = createStation({
        powerOperationalFreshUntil: new Date(Date.now() + 45_000).toISOString(),
        channelOperationalFreshUntil: new Date(Date.now() + 45_000).toISOString()
      });
      fleet.replace([initial]);
      await vi.advanceTimersByTimeAsync(30_000);

      fleet.commit([createStation({
        powerOperationalFreshUntil: new Date(Date.now() + 45_000).toISOString(),
        channelOperationalFreshUntil: new Date(Date.now() + 45_000).toISOString()
      })]);
      await vi.advanceTimersByTimeAsync(15_001);
      expect(fleet.stations[0].powerOperationallyFresh).toBe(true);
      expect(fleet.stations[0].channelOperationallyFresh).toBe(true);

      await vi.advanceTimersByTimeAsync(30_000);
      expect(fleet.stations[0].powerOperationallyFresh).toBe(false);
      expect(fleet.stations[0].channelOperationallyFresh).toBe(false);
    } finally {
      fleet.stopChannelMemoryExpiry();
      vi.useRealTimers();
    }
  });
});
