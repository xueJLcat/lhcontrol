import type { StationInfo } from '../types';
import { pushToast } from '../toast';
import { t } from '../i18n.svelte';

export interface AutoSleepEvent {
  id?: number;
  phase: 'started' | 'completed' | 'cancelled' | 'timed-out' | 'skipped' | 'failed';
  success?: number;
  unconfirmed?: number;
  failed?: number;
  skipped?: number;
  timedOut?: boolean;
  timedOutSkipped?: number;
  error?: string;
  updateId?: number;
  stations?: StationInfo[];
}

export interface AutoSleepEventDependencies {
  isDisposed(): boolean;
  setRunning(running: boolean): void;
  beginStatusOperation(): void;
  setStatusMessage(message: string): void;
  applyStations(updateId: number, stations: StationInfo[]): void;
}

export class AutoSleepEventCoordinator {
  private readonly activeActionIds = new Set<number>();
  private latestStatusId = 0;
  private latestStatusPhase: AutoSleepEvent['phase'] | null = null;

  constructor(private dependencies: AutoSleepEventDependencies) {}

  handle(event: AutoSleepEvent) {
    if (this.dependencies.isDisposed() || !event) return;
    if (event.phase !== 'started' && Array.isArray(event.stations)) {
      this.dependencies.applyStations(event.updateId ?? 0, event.stations);
    }

    const id = Number(event.id ?? 0);
    if (Number.isInteger(id) && id > 0) {
      if (event.phase === 'started') {
        // IDs increase when an action acquires the serialized auto-sleep
        // slot. A start at or below the latest displayed lifecycle is either
        // a duplicate or a delayed event whose terminal phase already won;
        // admitting it would leave the controls locked with no future
        // terminal event to remove it.
        if (id <= this.latestStatusId) return;
        this.activeActionIds.add(id);
      } else {
        // Older terminal events still release the action they own even when
        // their status copy is stale.
        this.activeActionIds.delete(id);
      }
      // Keep controls locked while any superseded action is still draining,
      // even if a newer replacement already finished or was skipped.
      this.dependencies.setRunning(this.activeActionIds.size > 0);
      // A terminal event from an older, cancelled watcher may arrive after a
      // replacement action started. Its station snapshot is still useful and
      // update-ID gated above, but its copy must not replace the newer status.
      if (id < this.latestStatusId) return;
      // A terminal phase is allowed to follow the matching start exactly
      // once. Duplicate or contradictory terminal deliveries for that same
      // action must not repeat toasts or rewrite a newer status operation.
      if (id === this.latestStatusId && this.latestStatusPhase !== 'started') return;
      this.latestStatusId = id;
      this.latestStatusPhase = event.phase;
    } else {
      // Compatibility with unsequenced events from older backends and tests.
      // When a sequenced action is already tracked it owns the running flag;
      // an unsequenced event must neither unlock it prematurely nor re-lock it
      // after it drained, so only act while no sequenced action is active.
      if (this.activeActionIds.size === 0) {
        this.dependencies.setRunning(event.phase === 'started');
      }
    }

    switch (event.phase) {
      case 'started':
        this.dependencies.beginStatusOperation();
        this.setMessage(t('Auto sleep: scanning and putting all stations to sleep...'));
        pushToast(t('Session ended — scanning and putting all stations to sleep.'), 'info');
        break;
      case 'completed':
        this.handleCompleted(event);
        break;
      case 'cancelled':
        this.handleCancelled(event);
        break;
      case 'timed-out':
        this.handleTimedOut(event);
        break;
      case 'skipped':
        this.dependencies.beginStatusOperation();
        this.setMessage(`${t('Auto sleep skipped')}: ${event.error || t('Bluetooth busy')}.`);
        pushToast(`${t('Auto sleep skipped')}: ${event.error || t('Bluetooth busy')}.`, 'info');
        break;
      case 'failed':
        this.dependencies.beginStatusOperation();
        this.setMessage(`${t('Auto sleep failed')}: ${event.error || t('unknown error')}.`);
        pushToast(`${t('Auto sleep failed')}: ${event.error || t('unknown error')}.`);
        break;
    }
  }

  private handleCompleted(event: AutoSleepEvent) {
    this.dependencies.beginStatusOperation();
    const success = event.success ?? 0;
    const unconfirmed = event.unconfirmed ?? 0;
    const failed = event.failed ?? 0;
    const skipped = event.skipped ?? 0;
    const message = t('Auto sleep finished: {success} confirmed, {unconfirmed} unconfirmed, {failed} failed, {skipped} skipped.', {
      success, unconfirmed, failed, skipped
    });
    this.setMessage(message);
    if (failed > 0 || unconfirmed > 0 || skipped > 0) {
      pushToast(message, 'warning');
    } else {
      pushToast(t('Auto sleep finished: {success} station(s) put to sleep.', { success }), 'success');
    }
  }

  private handleCancelled(event: AutoSleepEvent) {
    this.dependencies.beginStatusOperation();
    const success = event.success ?? 0;
    const unconfirmed = event.unconfirmed ?? 0;
    const failed = event.failed ?? 0;
    const skipped = event.skipped ?? 0;
    const details = success || unconfirmed || failed || skipped
      ? t('{success} confirmed, {unconfirmed} unconfirmed, {failed} failed, {skipped} skipped', { success, unconfirmed, failed, skipped })
      : t('no station commands completed');
    const message = t('Auto sleep cancelled: {details}.', { details });
    this.setMessage(message);
    pushToast(message, success || failed || unconfirmed || skipped ? 'warning' : 'info');
  }

  private handleTimedOut(event: AutoSleepEvent) {
    this.dependencies.beginStatusOperation();
    const success = event.success ?? 0;
    const unconfirmed = event.unconfirmed ?? 0;
    const failed = event.failed ?? 0;
    const timedOutSkipped = event.timedOutSkipped ?? 0;
    const skipped = event.skipped ?? 0;
    let message = t('Auto sleep timed out: {success} confirmed, {unconfirmed} unconfirmed, {failed} failed, {timedOutSkipped} skipped due to timeout.', {
      success, unconfirmed, failed, timedOutSkipped
    });
    if (skipped > 0) {
      message += ` ${t('{skipped} more skipped.', { skipped })}`;
    }
    this.setMessage(message);
    pushToast(message, 'warning');
  }

  private setMessage(message: string) {
    this.dependencies.setStatusMessage(message);
  }
}
