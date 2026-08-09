import { station as stationModels } from '../../../wailsjs/go/models';
import type { PowerTarget, StationInfo } from '../types';
import { ChannelMemory } from '../channel-memory';
import {
  canSetPower,
  hasCurrentChannel,
  hasStableConfirmedPowerState,
  sameStationInfo
} from '../station';

export class FleetState {
  stations = $state<StationInfo[]>([]);
  private channelMemory = new ChannelMemory();

  sortedStations = $derived([...this.stations].sort((a, b) => {
    const ac = this.displayChannel(a) || Number.MAX_SAFE_INTEGER;
    const bc = this.displayChannel(b) || Number.MAX_SAFE_INTEGER;
    return ac - bc || a.name.localeCompare(b.name) || a.address.localeCompare(b.address);
  }));

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
    return this.channelMemory.displayChannel(station);
  }

  syncChannelMemory() {
    this.channelMemory.refresh(this.stations);
  }

  replace(stations: StationInfo[]) {
    this.stations = stations;
  }

  commit(updated: StationInfo[]) {
    const previousByAddress = new Map(this.stations.map((station) => [station.address, station]));
    this.stations = updated.map((station) => {
      const previous = previousByAddress.get(station.address);
      return previous && sameStationInfo(previous, station) ? previous : station;
    });
  }

  merge(updated: StationInfo[]) {
    if (!updated.length) return;
    // The map dedupes by address (last wins) so a payload containing the same
    // new address twice cannot append duplicate cards and double-count them in
    // the fleet aggregates.
    const byAddress = new Map(updated.map((station) => [station.address, station]));
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

  occupiedChannelsExcluding(selectedAddress: string | null): Map<number, string[]> {
    const occupied = new Map<number, string[]>();
    const candidates = this.stations
      .filter((station) => hasCurrentChannel(station) && station.address !== selectedAddress)
      .sort((a, b) => a.name.localeCompare(b.name) || a.address.localeCompare(b.address));
    for (const station of candidates) {
      occupied.set(station.channel, [...(occupied.get(station.channel) ?? []), station.name]);
    }
    return occupied;
  }

  hasUnknownVisibleChannelExcluding(selectedAddress: string | null): boolean {
    return this.stations.some(
      (station) => station.isPresent && station.address !== selectedAddress &&
        (station.presenceUncertain || !station.scanFresh || !station.channelFresh || station.channel === 0)
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
