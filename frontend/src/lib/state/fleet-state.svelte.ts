import { station as stationModels } from '../../../wailsjs/go/models';
import type { PowerTarget, StationInfo } from '../types';
import { ChannelMemory } from '../channel-memory';
import {
  canSetPower,
  hasOperationallyCurrentChannel,
  hasStableConfirmedPowerState,
  sameStationInfo
} from '../station';

// Mirrors the backend operationSafetyFreshnessWindow: an operational deadline
// is always a backend read timestamp plus this window, so a legitimately
// scheduled expiry can never be more than this far ahead. Clamping the timer
// to it stops a backwards wall-clock adjustment from pinning the fresh flags
// (and the channel-conflict and duplicated-command guards that trust them)
// for the whole skew.
const OPERATIONAL_SAFETY_WINDOW_MS = 45_000;

export class FleetState {
  stations = $state<StationInfo[]>([]);
  private channelMemory = new ChannelMemory();
  // Bumped when a cached display channel expires so the shared display map
  // and every consumer re-evaluate even though the station list did not
  // change. Derived views only recompute when a reactive input changes, and
  // Date.now() alone is not one.
  private channelMemoryTick = $state(0);
  private channelMemoryTimer: ReturnType<typeof setTimeout> | null = null;
  private operationalFreshnessTimer: ReturnType<typeof setTimeout> | null = null;
  private operationalDeadlines = new Map<string, {
    power?: { source: string; due: number };
    channel?: { source: string; due: number };
  }>();

  channelDisplayByAddress = $derived.by(() => {
    void this.channelMemoryTick;
    return new Map(this.stations.map((station) => [
      station.address,
      this.channelMemory.displayChannel(station)
    ]));
  });

  sortedStations = $derived.by(() => {
    const displayChannels = this.channelDisplayByAddress;
    return [...this.stations].sort((a, b) => {
      const ac = displayChannels.get(a.address) || Number.MAX_SAFE_INTEGER;
      const bc = displayChannels.get(b.address) || Number.MAX_SAFE_INTEGER;
      return ac - bc || a.name.localeCompare(b.name) || a.address.localeCompare(b.address);
    });
  });

  private conflictStations = $derived(this.stations.filter((station) => station.channelConflict));
  conflictDetails = $derived((() => {
    const byChannel = new Map<number, string[]>();
    for (const station of this.conflictStations) {
      const key = station.channel > 0 ? station.channel : -1;
      byChannel.set(key, [...(byChannel.get(key) ?? []), station.name]);
    }
    return [...byChannel.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([channel, names]) => channel > 0 ? `CH ${channel}: ${names.join(' + ')}` : names.join(' + '))
      .join(' · ');
  })());

  fleetOn = $derived(this.countStable('on'));
  fleetStandby = $derived(this.countStable('standby'));
  fleetSleep = $derived(this.countStable('sleep'));
  fleetUnverified = $derived(Math.max(
    0,
    this.stations.length - this.fleetOn - this.fleetStandby - this.fleetSleep
  ));

  actionableOn = $derived(this.actionable('on'));
  actionableStandby = $derived(this.actionable('standby'));
  actionableSleep = $derived(this.actionable('sleep'));

  allOn = $derived(this.allStable('on'));
  allStandby = $derived(this.allStable('standby'));
  allSleep = $derived(this.allStable('sleep'));

  visibleCount = $derived(this.stations.filter(
    (station) => station.isPresent && !station.presenceUncertain
  ).length);
  invisibleCount = $derived(this.stations.filter((station) => !station.isPresent).length);
  uncertainCount = $derived(this.stations.filter(
    (station) => station.isPresent && station.presenceUncertain
  ).length);
  untrustedCount = $derived(this.stations.filter((station) =>
    !station.isPresent || station.presenceUncertain || !station.scanFresh ||
    !station.powerStateConfirmed || station.powerState < 0 || station.powerState === 3
  ).length);

  displayChannel(station: StationInfo): number {
    return this.channelDisplayByAddress.get(station.address) ?? station.channel;
  }

  syncChannelMemory() {
    this.channelMemory.refresh(this.stations);
    this.scheduleChannelMemoryExpiry();
  }

  // Re-render derived views when the nearest cached channel expires. Views are
  // only re-evaluated when a reactive input changes, so an expiry alone never
  // re-renders them; bumping the tick (and pruning) at the nearest expiry does.
  private scheduleChannelMemoryExpiry() {
    if (this.channelMemoryTimer !== null) {
      clearTimeout(this.channelMemoryTimer);
      this.channelMemoryTimer = null;
    }
    const delay = this.channelMemory.pruneExpired();
    if (delay === Number.POSITIVE_INFINITY) return;
    this.channelMemoryTimer = setTimeout(() => {
      this.channelMemoryTimer = null;
      this.channelMemoryTick += 1;
      this.scheduleChannelMemoryExpiry();
    }, delay);
  }

  stopChannelMemoryExpiry() {
    if (this.channelMemoryTimer !== null) {
      clearTimeout(this.channelMemoryTimer);
      this.channelMemoryTimer = null;
    }
    if (this.operationalFreshnessTimer !== null) {
      clearTimeout(this.operationalFreshnessTimer);
      this.operationalFreshnessTimer = null;
    }
  }

  replace(stations: StationInfo[]) {
    this.commit(stations);
  }

  commit(updated: StationInfo[]) {
    // A snapshot carrying the same address twice must not append duplicate
    // cards (the grid is keyed by address) or double-count the fleet
    // aggregates; an entry without an address is a ghost card, matching
    // merge's filter.
    const deduped = new Map(
      updated.filter((station) => Boolean(station?.address)).map((station) => [station.address, station])
    );
    // A late list response can contain the same read that already expired in
    // this view. Keep its elapsed-time deadline authoritative even after the
    // visible flag was cleared, including when the wall clock moved back.
    const now = performance.now();
    for (const [address, station] of deduped) {
      const deadlines = this.operationalDeadlines.get(address);
      const expirePower = station.powerOperationallyFresh &&
        deadlines?.power?.source === station.powerOperationalFreshUntil && deadlines.power.due <= now;
      const expireChannel = station.channelOperationallyFresh &&
        deadlines?.channel?.source === station.channelOperationalFreshUntil && deadlines.channel.due <= now;
      if (expirePower || expireChannel) {
        deduped.set(address, this.patch(station, {
          powerOperationallyFresh: expirePower ? false : station.powerOperationallyFresh,
          channelOperationallyFresh: expireChannel ? false : station.channelOperationallyFresh
        }));
      }
    }
    // A single-station response cannot update the peer's old conflict flag.
    // Recompute this fleet property after every merge and freshness expiry,
    // using the same operational evidence as channel-change validation.
    const counts = new Map<number, number>();
    for (const station of deduped.values()) {
      if (hasOperationallyCurrentChannel(station)) {
        counts.set(station.channel, (counts.get(station.channel) ?? 0) + 1);
      }
    }
    const previousByAddress = new Map(this.stations.map((station) => [station.address, station]));
    this.stations = [...deduped.values()].map((station) => {
      const conflict = hasOperationallyCurrentChannel(station) && (counts.get(station.channel) ?? 0) > 1;
      if (station.channelConflict !== conflict) station = this.patch(station, { channelConflict: conflict });
      const previous = previousByAddress.get(station.address);
      return previous && sameStationInfo(previous, station) ? previous : station;
    });
    this.scheduleOperationalFreshnessExpiry();
  }

  merge(updated: StationInfo[]) {
    if (!updated.length) return;
    // A degraded result can carry an empty station snapshot; merging it
    // would add a ghost card without an address and skew fleet aggregates.
    const valid = updated.filter((station) => Boolean(station?.address));
    if (!valid.length) return;
    // The map dedupes by address (last wins) so a payload containing the same
    // new address twice cannot append duplicate cards and double-count them in
    // the fleet aggregates.
    const byAddress = new Map(valid.map((station) => [station.address, station]));
    const existingAddresses = new Set(this.stations.map((station) => station.address));
    const newStations = [...byAddress.values()].filter((station) => !existingAddresses.has(station.address));
    this.commit([
      ...this.stations.map((station) => byAddress.get(station.address) ?? station),
      ...newStations
    ]);
  }

  patch(current: StationInfo, changes: Partial<StationInfo>): StationInfo {
    return stationModels.StationInfo.createFrom({ ...current, ...changes });
  }

  // Operation freshness is deliberately shorter than display freshness. A
  // long polling interval can cross the write-safety deadline without another
  // backend snapshot. Convert each backend timestamp to an elapsed-time
  // deadline when received; retaining that deadline across partial commits
  // prevents clock changes and unrelated updates from extending stale flags.
  private scheduleOperationalFreshnessExpiry() {
    if (this.operationalFreshnessTimer !== null) {
      clearTimeout(this.operationalFreshnessTimer);
      this.operationalFreshnessTimer = null;
    }
    const previousDeadlines = this.operationalDeadlines;
    const nextDeadlines = new Map<string, {
      power?: { source: string; due: number };
      channel?: { source: string; due: number };
    }>();
    const now = performance.now();
    const wallNow = Date.now();
    let nearest = Number.POSITIVE_INFINITY;
    for (const station of this.stations) {
      const previous = previousDeadlines.get(station.address);
      const deadlines: {
        power?: { source: string; due: number };
        channel?: { source: string; due: number };
      } = { ...previous };
      if (station.powerOperationallyFresh) {
        deadlines.power = this.monotonicDeadline(
          station.powerOperationalFreshUntil, previous?.power, now, wallNow
        );
        nearest = Math.min(nearest, deadlines.power.due);
      }
      if (station.channelOperationallyFresh) {
        deadlines.channel = this.monotonicDeadline(
          station.channelOperationalFreshUntil, previous?.channel, now, wallNow
        );
        nearest = Math.min(nearest, deadlines.channel.due);
      }
      if (deadlines.power || deadlines.channel) nextDeadlines.set(station.address, deadlines);
    }
    this.operationalDeadlines = nextDeadlines;
    if (nearest === Number.POSITIVE_INFINITY) return;
    const delay = Math.max(0, nearest - performance.now());
    this.operationalFreshnessTimer = setTimeout(() => {
      this.operationalFreshnessTimer = null;
      this.expireOperationalFreshnessThrough(performance.now());
    }, delay);
  }

  private monotonicDeadline(
    source: string,
    previous: { source: string; due: number } | undefined,
    now: number,
    wallNow: number
  ): { source: string; due: number } {
    if (previous?.source === source) return previous;
    const remaining = Math.min(
      OPERATIONAL_SAFETY_WINDOW_MS,
      Math.max(0, this.parseOperationalFreshUntil(source) - wallNow)
    );
    return { source, due: now + remaining };
  }

  private parseOperationalFreshUntil(value: string): number {
    const parsed = value ? Date.parse(value) : Number.NaN;
    // A true flag without a usable deadline cannot be kept alive safely. Use
    // -Infinity so the next zero-delay expiry pass downgrades it immediately.
    return Number.isFinite(parsed) ? parsed : Number.NEGATIVE_INFINITY;
  }

  private expireOperationalFreshnessThrough(deadline: number) {
    let changed = false;
    const updated = this.stations.map((station) => {
      const deadlines = this.operationalDeadlines.get(station.address);
      const expirePower = station.powerOperationallyFresh &&
        (deadlines?.power?.due ?? Number.NEGATIVE_INFINITY) <= deadline;
      const expireChannel = station.channelOperationallyFresh &&
        (deadlines?.channel?.due ?? Number.NEGATIVE_INFINITY) <= deadline;
      if (!expirePower && !expireChannel) return station;
      changed = true;
      return this.patch(station, {
        powerOperationallyFresh: expirePower ? false : station.powerOperationallyFresh,
        channelOperationallyFresh: expireChannel ? false : station.channelOperationallyFresh
      });
    });
    if (changed) {
      this.commit(updated);
    } else {
      this.scheduleOperationalFreshnessExpiry();
    }
  }

  occupiedChannelsExcluding(selectedAddress: string | null): Map<number, string[]> {
    const occupied = new Map<number, string[]>();
    const candidates = this.stations
      // Only an operation-fresh read can prove occupancy. Older display data
      // remains visible, but is handled by the explicit unknown-risk flow.
      .filter((station) => hasOperationallyCurrentChannel(station) && station.address !== selectedAddress)
      .sort((a, b) => a.name.localeCompare(b.name) || a.address.localeCompare(b.address));
    for (const station of candidates) {
      occupied.set(station.channel, [...(occupied.get(station.channel) ?? []), station.name]);
    }
    return occupied;
  }

  hasUnknownVisibleChannelExcluding(selectedAddress: string | null): boolean {
    return this.stations.some(
      (station) => station.isPresent && station.address !== selectedAddress &&
        !hasOperationallyCurrentChannel(station)
    );
  }

  private countStable(target: PowerTarget): number {
    return this.stations.filter((station) => hasStableConfirmedPowerState(station, target)).length;
  }

  private actionable(target: PowerTarget): StationInfo[] {
    return this.stations.filter((station) => canSetPower(station, target));
  }

  private allStable(target: PowerTarget): boolean {
    return this.stations.length > 0 && this.stations.every(
      (station) => hasStableConfirmedPowerState(station, target)
    );
  }
}
