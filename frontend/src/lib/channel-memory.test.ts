import { afterEach, describe, expect, it, vi } from 'vitest';
import { ChannelMemory } from './channel-memory';
import type { StationInfo } from './types';
import { createStation } from '../test/fixtures';

function withChannel(channel: number, overrides: Partial<StationInfo> = {}): StationInfo {
  return createStation({ channel, ...overrides });
}

describe('ChannelMemory', () => {
  it('prefers the live channel when present', () => {
    const memory = new ChannelMemory();
    memory.refresh([withChannel(5)]);
    expect(memory.displayChannel(withChannel(7))).toBe(7);
  });

  it('bridges a transient channel wipe until the TTL expires', () => {
    vi.useFakeTimers();
    try {
      const memory = new ChannelMemory(45_000);
      memory.refresh([withChannel(3)]);
      const wiped = withChannel(0, { channelFresh: false });
      memory.refresh([wiped]);

      expect(memory.displayChannel(wiped)).toBe(3);
      vi.advanceTimersByTime(44_999);
      expect(memory.displayChannel(wiped)).toBe(3);
      vi.advanceTimersByTime(2);
      expect(memory.displayChannel(wiped)).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it('drops entries for stations that left the list', () => {
    const memory = new ChannelMemory();
    memory.refresh([withChannel(3)]);
    memory.refresh([]);
    expect(memory.displayChannel(withChannel(0))).toBe(0);
  });

  it('keeps independent memory per address', () => {
    const memory = new ChannelMemory();
    memory.refresh([
      withChannel(2, { address: 'AA' }),
      withChannel(9, { address: 'BB' })
    ]);
    memory.refresh([
      withChannel(0, { address: 'AA' }),
      withChannel(0, { address: 'BB' })
    ]);
    expect(memory.displayChannel(withChannel(0, { address: 'AA' }))).toBe(2);
    expect(memory.displayChannel(withChannel(0, { address: 'BB' }))).toBe(9);
  });

  it('pruneExpired drops expired entries and reports the nearest expiry', () => {
    vi.useFakeTimers();
    try {
      const memory = new ChannelMemory(45_000);
      expect(memory.pruneExpired()).toBe(Number.POSITIVE_INFINITY);

      memory.refresh([withChannel(3, { address: 'AA' })]);
      // Nothing has expired yet; the nearest expiry is a full TTL away.
      let next = memory.pruneExpired();
      expect(next).toBeGreaterThan(0);
      expect(next).toBeLessThanOrEqual(45_000);
      expect(memory.displayChannel(withChannel(0, { address: 'AA' }))).toBe(3);

      vi.advanceTimersByTime(45_001);
      expect(memory.pruneExpired()).toBe(Number.POSITIVE_INFINITY);
      expect(memory.displayChannel(withChannel(0, { address: 'AA' }))).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });
});
