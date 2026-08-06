// Epoch and revision gates that keep stale asynchronous results from
// overwriting newer UI state. Every async entry point captures the epochs it
// started under and must pass the matching commit checks before writing back.
export class OperationGate {
  private disposed = false;
  private statusEpoch = 0;
  private scanEpoch = 0;
  private nextStationRevision = 0;
  private stationRevisions = new Map<string, number>();
  // Operation ownership tokens survive a scan-epoch bump (unlike
  // stationRevisions), so an operation settling mid-scan can still release
  // the busy flags it owns. A newer operation on the same station overwrites
  // the token and protects its own flags from the older operation's cleanup.
  private stationOpTokens = new Map<string, number>();

  dispose() {
    this.disposed = true;
  }

  beginStatusOperation(): number {
    return ++this.statusEpoch;
  }

  get currentStatusEpoch(): number {
    return this.statusEpoch;
  }

  canCommitStatus(epoch: number): boolean {
    return !this.disposed && epoch === this.statusEpoch;
  }

  beginScanEpoch(): number {
    this.scanEpoch += 1;
    this.stationRevisions = new Map();
    return this.scanEpoch;
  }

  get currentScanEpoch(): number {
    return this.scanEpoch;
  }

  canCommitOperation(epoch: number): boolean {
    return !this.disposed && epoch === this.scanEpoch;
  }

  snapshotStationRevisions(): Map<string, number> {
    return new Map(this.stationRevisions);
  }

  stationRevision(address: string): number {
    return this.stationRevisions.get(address) ?? 0;
  }

  beginStationOperationRevision(address: string): number {
    const revision = ++this.nextStationRevision;
    this.stationRevisions = new Map(this.stationRevisions).set(address, revision);
    this.stationOpTokens.set(address, revision);
    return revision;
  }

  canCommitStationOperation(epoch: number, address: string, revision: number): boolean {
    return this.canCommitOperation(epoch) && this.stationRevision(address) === revision;
  }

  canCleanupStationOperation(address: string, revision: number): boolean {
    return !this.disposed && this.stationOpTokens.get(address) === revision;
  }

  clearOperationTokens() {
    this.stationOpTokens.clear();
  }
}
