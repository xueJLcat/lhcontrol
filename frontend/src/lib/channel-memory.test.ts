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

  it('starts the TTL when the channel first disappears, not when it was last observed', () => {
    vi.useFakeTimers();
    try {
      const memory = new ChannelMemory(45_000);
      memory.refresh([withChannel(3)]);

      // A healthy channel can remain unchanged for much longer than the
      // display-memory TTL before a transient read wipes it.
      vi.advanceTimersByTime(60_000);
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

  it('pruneExpired only schedules and drops entries after their channel disappears', () => {
    vi.useFakeTimers();
    try {
      const memory = new ChannelMemory(45_000);
      expect(memory.pruneExpired()).toBe(Number.POSITIVE_INFINITY);

      memory.refresh([withChannel(3, { address: 'AA' })]);
      // Live channels remain useful indefinitely and need no expiry timer.
      vi.advanceTimersByTime(60_000);
      expect(memory.pruneExpired()).toBe(Number.POSITIVE_INFINITY);

      const wiped = withChannel(0, { address: 'AA', channelFresh: false });
      memory.refresh([wiped]);
      let next = memory.pruneExpired();
      expect(next).toBeGreaterThan(0);
      expect(next).toBeLessThanOrEqual(45_000);
      expect(memory.displayChannel(wiped)).toBe(3);

      vi.advanceTimersByTime(45_001);
      expect(memory.pruneExpired()).toBe(Number.POSITIVE_INFINITY);
      expect(memory.displayChannel(wiped)).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });
});
