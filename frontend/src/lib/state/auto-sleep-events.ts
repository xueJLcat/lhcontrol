import type { StationInfo } from '../types';
import { pushToast } from '../toast';
import { t } from '../i18n.svelte';

export interface AutoSleepEvent {
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
  constructor(private dependencies: AutoSleepEventDependencies) {}

  handle(event: AutoSleepEvent) {
    if (this.dependencies.isDisposed() || !event) return;
    if (event.phase !== 'started' && Array.isArray(event.stations)) {
      this.dependencies.applyStations(event.updateId ?? 0, event.stations);
    }

    switch (event.phase) {
      case 'started':
        this.dependencies.setRunning(true);
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
        this.dependencies.setRunning(false);
        this.dependencies.beginStatusOperation();
        this.setMessage(`${t('Auto sleep skipped')}: ${event.error || t('Bluetooth busy')}.`);
        pushToast(`${t('Auto sleep skipped')}: ${event.error || t('Bluetooth busy')}.`, 'info');
        break;
      case 'failed':
        this.dependencies.setRunning(false);
        this.dependencies.beginStatusOperation();
        this.setMessage(`${t('Auto sleep failed')}: ${event.error || t('unknown error')}.`);
        pushToast(`${t('Auto sleep failed')}: ${event.error || t('unknown error')}.`);
        break;
    }
  }

  private handleCompleted(event: AutoSleepEvent) {
    this.dependencies.setRunning(false);
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
    this.dependencies.setRunning(false);
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
    pushToast(message, success || failed ? 'warning' : 'info');
  }

  private handleTimedOut(event: AutoSleepEvent) {
    this.dependencies.setRunning(false);
    this.dependencies.beginStatusOperation();
    const success = event.success ?? 0;
    const unconfirmed = event.unconfirmed ?? 0;
    const failed = event.failed ?? 0;
    const timedOutSkipped = event.timedOutSkipped ?? 0;
    const message = t('Auto sleep timed out: {success} confirmed, {unconfirmed} unconfirmed, {failed} failed, {timedOutSkipped} skipped due to timeout.', {
      success, unconfirmed, failed, timedOutSkipped
    });
    this.setMessage(message);
    pushToast(message, 'warning');
  }

  private setMessage(message: string) {
    this.dependencies.setStatusMessage(message);
  }
}
