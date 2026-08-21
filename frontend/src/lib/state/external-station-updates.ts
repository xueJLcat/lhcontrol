import type { StationInfo } from '../types';

export interface ExternalStationUpdateEvent {
  id: number;
  source: string;
  stations: StationInfo[];
}

export interface ExternalStationUpdateDependencies {
  isDisposed(): boolean;
  localListOperationOwnsSnapshot(): boolean;
  isStationBusy(address: string): boolean;
  invalidatePendingLists(): void;
  mergeStations(stations: StationInfo[]): void;
}

export class ExternalStationUpdateCoordinator {
  private lastUpdateId = 0;

  constructor(private dependencies: ExternalStationUpdateDependencies) {}

  handle(event: ExternalStationUpdateEvent) {
    if (this.dependencies.isDisposed() || !event || !Array.isArray(event.stations)) return;
    // A non-numeric id would slip through the monotonic gate as "unsequenced"
    // and merge without advancing it; the backend sequencer always emits
    // finite numbers, so drop anything else as a malformed event.
    if (!Number.isFinite(event.id)) return;
    this.apply(event.id, event.stations);
  }

  apply(id: number, stations: StationInfo[]) {
    if (id > 0 && id <= this.lastUpdateId) return;
    if (id > 0) this.lastUpdateId = id;

    // An HTTP or automatic-sleep snapshot can already be queued when a local
    // scan or bulk operation is accepted. Keep the event id monotonic while
    // leaving that newer local result as the authoritative list owner.
    if (this.dependencies.localListOperationOwnsSnapshot()) return;

    // Invalidate any list request that began before this event. A delayed
    // periodic response must not restore the snapshot from before an HTTP or
    // automatic-sleep operation completed.
    this.dependencies.invalidatePendingLists();

    // A local operation owns its station snapshot until its promise settles.
    // Skipped updates are recovered by that result or the periodic poll.
    const safeUpdates = stations.filter((station) =>
      Boolean(station?.address) && !this.dependencies.isStationBusy(station.address)
    );
    this.dependencies.mergeStations(safeUpdates);
  }
}
