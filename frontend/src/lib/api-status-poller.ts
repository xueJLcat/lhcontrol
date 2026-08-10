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
// host only sees warnings it has not been told about yet. Revision-gated so
// an out-of-order response can never overwrite a newer poll.
export class ApiStatusPoller {
  private readonly revisions = new RevisionGate();
  private readonly reportedWarnings = new Set<string>();
  private interval: ReturnType<typeof setInterval> | null = null;

  constructor(private readonly host: ApiStatusPollerHost) {}

  start(intervalMs = 15000): Promise<void> {
    if (this.interval) clearInterval(this.interval);
    const initialRefresh = this.refresh();
    this.interval = setInterval(() => this.refresh(), intervalMs);
    return initialRefresh;
  }

  refresh(): Promise<void> {
    const revision = this.revisions.next();
    return GetAPIStatus().then((status) => {
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
  }

  dispose() {
    if (this.interval) clearInterval(this.interval);
    this.interval = null;
    this.revisions.dispose();
  }
}
