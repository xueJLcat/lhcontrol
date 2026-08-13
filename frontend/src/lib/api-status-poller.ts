import type { main } from '../../wailsjs/go/models';
import { GetAPIStatus } from './backend';
import { RevisionGate } from './revision-gate';

export interface ApiStatusPollerHost {
  isDisposed(): boolean;
  commitStatus(status: main.APIStatus): void;
  commitFailure(error: string): void;
  reportConfigWarning(warning: string): void;
}

// Polls the HTTP API health and owns the config-warning de-duplication so the
// host only sees warnings it has not been told about yet. Requests are
// serialized and concurrent refreshes are coalesced into one trailing poll so
// a slow API cannot make every response stale indefinitely.
export class ApiStatusPoller {
  private readonly revisions = new RevisionGate();
  private readonly reportedWarnings = new Set<string>();
  private interval: ReturnType<typeof setInterval> | null = null;
  private inFlight: Promise<void> | null = null;
  private trailingRefresh: Promise<void> | null = null;
  private queueGeneration = 0;
  private disposed = false;

  constructor(private readonly host: ApiStatusPollerHost) {}

  start(intervalMs = 15000): Promise<void> {
    if (this.disposed || this.host.isDisposed()) return Promise.resolve();
    if (this.interval) clearInterval(this.interval);
    // An explicit restart usually means the persisted interval or listener
    // configuration changed. It must not wait forever behind a hung request
    // from the previous schedule, so supersede that generation immediately.
    // RevisionGate prevents its eventual response from committing.
    this.queueGeneration += 1;
    this.trailingRefresh = null;
    const initialRefresh = this.beginRefresh();
    this.interval = setInterval(() => this.refresh(), intervalMs);
    return initialRefresh;
  }

  refresh(): Promise<void> {
    if (this.disposed || this.host.isDisposed()) return Promise.resolve();
    if (this.trailingRefresh) return this.trailingRefresh;
    if (this.inFlight) {
      const current = this.inFlight;
      const generation = this.queueGeneration;
      const trailing = current.then(() => {
        if (this.trailingRefresh === trailing) this.trailingRefresh = null;
        if (this.disposed || this.host.isDisposed() || generation !== this.queueGeneration) return;
        return this.beginRefresh();
      });
      this.trailingRefresh = trailing;
      return trailing;
    }
    return this.beginRefresh();
  }

  private beginRefresh(): Promise<void> {
    const revision = this.revisions.next();
    const request = GetAPIStatus().then((status) => {
      if (this.host.isDisposed() || !this.revisions.isCurrent(revision)) return;
      this.host.commitStatus(status);
      const current = new Set(status.warnings ?? []);
      for (const warning of this.reportedWarnings) {
        if (!current.has(warning)) this.reportedWarnings.delete(warning);
      }
      for (const warning of status.warnings ?? []) {
        if (this.reportedWarnings.has(warning)) continue;
        this.reportedWarnings.add(warning);
        this.host.reportConfigWarning(warning);
      }
    }).catch((error) => {
      if (this.host.isDisposed() || !this.revisions.isCurrent(revision)) return;
      this.host.commitFailure(String(error));
      // Config health is intentionally left untouched: an API outage must not
      // fabricate a "config writable" state. The host hides the config pill
      // while the API is offline, and the next successful poll reconciles.
    });
    this.inFlight = request;
    void request.then(() => {
      if (this.inFlight === request) this.inFlight = null;
    });
    return request;
  }

  dispose() {
    this.disposed = true;
    if (this.interval) clearInterval(this.interval);
    this.interval = null;
    this.revisions.dispose();
  }
}
