import type { main } from '../../wailsjs/go/models';
import { GetAPIStatus } from './backend';
import { RevisionGate } from './revision-gate';

export interface ApiStatusPollerHost {
  isDisposed(): boolean;
  commitStatus(status: main.APIStatus): void;
  commitFailure(error: string): void;
  reportConfigWarning(warning: string): void;
}

interface RefreshCompletion {
  promise: Promise<void>;
  resolve(): void;
}

function createRefreshCompletion(): RefreshCompletion {
  let resolve!: () => void;
  const promise = new Promise<void>((settle) => { resolve = settle; });
  return { promise, resolve };
}

// Polls the HTTP API health and owns the config-warning de-duplication so the
// host only sees warnings it has not been told about yet. Requests are
// serialized and concurrent refreshes are coalesced into one trailing poll so
// a slow API cannot make every response stale indefinitely.
export class ApiStatusPoller {
  private readonly revisions = new RevisionGate();
  private readonly reportedWarnings = new Set<string>();
  private interval: ReturnType<typeof setInterval> | null = null;
  private activeRevision: number | null = null;
  private activeCompletions: RefreshCompletion[] = [];
  private trailingCompletion: RefreshCompletion | null = null;
  private disposed = false;

  constructor(private readonly host: ApiStatusPollerHost) {}

  start(intervalMs = 15000): Promise<void> {
    if (this.disposed || this.host.isDisposed()) return Promise.resolve();
    if (this.interval) clearInterval(this.interval);
    // An explicit restart usually means the persisted interval or listener
    // configuration changed. It must not wait forever behind a hung request
    // from the previous schedule, so supersede it immediately. Carry every
    // logical caller onto the replacement request; otherwise settings saves
    // awaiting the old request can remain busy forever.
    const completion = createRefreshCompletion();
    const carried = [...this.activeCompletions];
    if (this.trailingCompletion) carried.push(this.trailingCompletion);
    carried.push(completion);
    this.trailingCompletion = null;
    this.beginRefresh(carried);
    this.interval = setInterval(() => this.refresh(), intervalMs);
    return completion.promise;
  }

  refresh(): Promise<void> {
    if (this.disposed || this.host.isDisposed()) return Promise.resolve();
    if (this.trailingCompletion) return this.trailingCompletion.promise;
    if (this.activeRevision !== null) {
      this.trailingCompletion = createRefreshCompletion();
      return this.trailingCompletion.promise;
    }
    const completion = createRefreshCompletion();
    this.beginRefresh([completion]);
    return completion.promise;
  }

  private beginRefresh(completions: RefreshCompletion[]): void {
    const revision = this.revisions.next();
    this.activeRevision = revision;
    this.activeCompletions = completions;

    const commitFailure = (error: unknown) => {
      if (this.host.isDisposed() || !this.revisions.isCurrent(revision)) return;
      this.host.commitFailure(String(error));
      // Config health is intentionally left untouched: an API outage must not
      // fabricate a "config writable" state. The host hides the config pill
      // while the API is offline, and the next successful poll reconciles.
    };

    let request: Promise<void>;
    try {
      request = GetAPIStatus().then((status) => {
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
      }, commitFailure);
    } catch (error) {
      request = Promise.resolve().then(() => commitFailure(error));
    }

    // Completion bookkeeping must run even if a host callback throws. The
    // physical request is never exposed directly to callers.
    void request.then(
      () => this.finishRefresh(revision),
      () => this.finishRefresh(revision)
    );
  }

  private finishRefresh(revision: number): void {
    if (this.activeRevision !== revision) return;

    const completed = this.activeCompletions;
    this.activeRevision = null;
    this.activeCompletions = [];

    if (this.disposed || this.host.isDisposed()) {
      if (this.trailingCompletion) completed.push(this.trailingCompletion);
      this.trailingCompletion = null;
      this.resolveCompletions(completed);
      return;
    }

    const trailing = this.trailingCompletion;
    this.trailingCompletion = null;
    if (trailing) this.beginRefresh([trailing]);
    this.resolveCompletions(completed);
  }

  private resolveCompletions(completions: RefreshCompletion[]): void {
    for (const completion of completions) completion.resolve();
  }

  dispose() {
    this.disposed = true;
    if (this.interval) clearInterval(this.interval);
    this.interval = null;
    this.revisions.dispose();
    const pending = this.activeCompletions;
    if (this.trailingCompletion) pending.push(this.trailingCompletion);
    this.activeRevision = null;
    this.activeCompletions = [];
    this.trailingCompletion = null;
    this.resolveCompletions(pending);
  }
}
