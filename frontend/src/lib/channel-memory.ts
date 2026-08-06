import type { StationInfo } from './types';

// Display-only channel memory. The backend deliberately wipes a station's
// channel on transient capability loss (rediscovery, read errors), which made
// the channel bar, card chip, and card ordering flap between enabled/disabled
// on every background refresh. The cache bridges those dropouts for display
// only; conflict checks, freshness gates, and the channel modal always use
// the live station.channel.
export class ChannelMemory {
  private readonly entries = new Map<string, { channel: number; at: number }>();

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
        this.entries.set(station.address, { channel: station.channel, at: now });
      }
    }
    for (const cached of [...this.entries.keys()]) {
      if (!present.has(cached)) this.entries.delete(cached);
    }
  }

  displayChannel(station: StationInfo): number {
    if (station.channel > 0) return station.channel;
    const cached = this.entries.get(station.address);
    return cached && Date.now() - cached.at <= this.ttlMs ? cached.channel : 0;
  }
}
