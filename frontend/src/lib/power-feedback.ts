import type { PowerFeedback, StationInfo } from './types';
import { powerStateValue } from './station';

const RETENTION_MS = 20_000;
// Pending feedback is normally replaced when its operation settles. If that
// commit is legitimately dropped (a scan epoch superseded it), expire the
// stale pending note instead of showing "Switching..." forever.
const PENDING_RETENTION_MS = 60_000;

// Per-station power feedback notes shown on the cards. The registry owns the
// retention timers and publishes an immutable snapshot through onChange so
// the component can keep a plain reactive record.
export class PowerFeedbackRegistry {
  private record: Record<string, PowerFeedback | undefined> = {};
  private timers = new Map<string, ReturnType<typeof setTimeout>>();

  constructor(private readonly onChange: (next: Record<string, PowerFeedback | undefined>) => void) {}

  set(address: string, feedback: Omit<PowerFeedback, 'createdAt'>) {
    this.clearTimer(address);
    const createdAt = Date.now();
    this.record = {
      ...this.record,
      [address]: { ...feedback, createdAt }
    };
    this.onChange(this.record);
    const retentionMs = feedback.kind === 'pending' ? PENDING_RETENTION_MS : RETENTION_MS;
    this.timers.set(address, setTimeout(() => {
      if (this.record[address]?.createdAt === createdAt) {
        this.clear(address);
      }
    }, retentionMs));
  }

  clear(address: string) {
    this.clearTimer(address);
    if (!this.record[address]) return;
    const next = { ...this.record };
    delete next[address];
    this.record = next;
    this.onChange(next);
  }

  clearAll() {
    for (const timer of this.timers.values()) clearTimeout(timer);
    this.timers.clear();
    this.record = {};
    this.onChange(this.record);
  }

  // Keeps feedback only for addresses that still own a live operation. A scan
  // that preserves in-flight station operations must not clear their visible
  // pending notes; entries for settled operations are dropped.
  retain(addresses: ReadonlySet<string>) {
    for (const address of Object.keys(this.record)) {
      if (!addresses.has(address)) this.clear(address);
    }
  }

  // Drops settled feedback once a newer authoritative read supersedes it.
  reconcile(updated: StationInfo[]) {
    for (const station of updated) {
      const feedback = this.record[station.address];
      if (!feedback || feedback.kind === 'pending') continue;
      const targetChanged = feedback.target !== undefined &&
        station.powerFresh && station.powerStateConfirmed &&
        station.powerState !== powerStateValue(feedback.target);
      const feedbackReadAt = feedback.readAt ? Date.parse(feedback.readAt) : Number.NaN;
      const stationReadAt = station.lastPowerReadAt ? Date.parse(station.lastPowerReadAt) : Number.NaN;
      const newerRead = Number.isFinite(feedbackReadAt) && Number.isFinite(stationReadAt) &&
        stationReadAt > feedbackReadAt;
      if (targetChanged || newerRead) this.clear(station.address);
    }
  }

  private clearTimer(address: string) {
    const timer = this.timers.get(address);
    if (timer) clearTimeout(timer);
    this.timers.delete(address);
  }
}
