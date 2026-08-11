import type { StationInfo } from './types';

// Display-only channel memory. The backend deliberately wipes a station's
// channel on transient capability loss (rediscovery, read errors), which made
// the channel bar, card chip, and card ordering flap between enabled/disabled
// on every background refresh. The cache bridges those dropouts for display
// only; conflict checks, freshness gates, and the channel modal always use
// the live station.channel.
export class ChannelMemory {
  private readonly entries = new Map<string, {
    channel: number;
    missingSince: number | null;
  }>();

  constructor(private readonly ttlMs = 45_000) {}

  // Keeps the memory current before derived lists recompute and drops
  // entries for stations that left the list so long sessions do not
  // accumulate stale addresses.
  refresh(stations: StationInfo[]) {
    const now = Date.now();
    const present = new Set<string>();
    for (const station of stations) {
      present.add(station.address);
      if (station.channel > 0) {
        // A live channel is not a transient value and must never age out. The
        // retention window starts only when a later snapshot first loses it.
        this.entries.set(station.address, {
          channel: station.channel,
          missingSince: null
        });
      } else {
        const cached = this.entries.get(station.address);
        if (cached?.missingSince === null) {
          this.entries.set(station.address, { ...cached, missingSince: now });
        }
      }
    }
    for (const cached of [...this.entries.keys()]) {
      if (!present.has(cached)) this.entries.delete(cached);
    }
  }

  displayChannel(station: StationInfo): number {
    if (station.channel > 0) return station.channel;
    const cached = this.entries.get(station.address);
    if (!cached) return 0;
    return cached.missingSince === null || Date.now() - cached.missingSince <= this.ttlMs
      ? cached.channel
      : 0;
  }

  // Drops expired entries and returns the milliseconds until the nearest
  // missing-channel expiry (Infinity when no cached channel is currently
  // missing). Derived views are only re-evaluated when their reactive inputs
  // change, so an expiry alone would never re-render them; the caller schedules
  // a tick at this delay to do so.
  pruneExpired(): number {
    const now = Date.now();
    let nextExpiry = Number.POSITIVE_INFINITY;
    for (const [address, entry] of [...this.entries]) {
      if (entry.missingSince === null) continue;
      const remaining = entry.missingSince + this.ttlMs - now;
      if (remaining <= 0) {
        this.entries.delete(address);
      } else if (remaining < nextExpiry) {
        nextExpiry = remaining;
      }
    }
    return nextExpiry;
  }
}
