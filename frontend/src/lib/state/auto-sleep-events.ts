import type { StationInfo } from '../types';
import { pushToast } from '../toast';
import { t } from '../i18n.svelte';

export interface AutoSleepEvent {
  phase: 'started' | 'completed' | 'cancelled' | 'skipped' | 'failed';
  success?: number;
  failed?: number;
  skipped?: number;
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
    const failed = event.failed ?? 0;
    const skipped = event.skipped ?? 0;
    const message = t('Auto sleep finished: {success} succeeded, {failed} failed, {skipped} skipped.', {
      success, failed, skipped
    });
    this.setMessage(message);
    if (failed > 0) {
      pushToast(skipped
        ? message
        : t('Auto sleep finished: {success} succeeded, {failed} failed.', { success, failed }), 'warning');
    } else if (skipped > 0) {
      pushToast(t('Auto sleep finished: {success} succeeded, {skipped} skipped.', { success, skipped }), 'warning');
    } else {
      pushToast(t('Auto sleep finished: {success} station(s) put to sleep.', { success }), 'success');
    }
  }

  private handleCancelled(event: AutoSleepEvent) {
    this.dependencies.setRunning(false);
    this.dependencies.beginStatusOperation();
    const success = event.success ?? 0;
    const failed = event.failed ?? 0;
    const skipped = event.skipped ?? 0;
    const details = success || failed || skipped
      ? t('{success} succeeded, {failed} failed, {skipped} skipped', { success, failed, skipped })
      : t('no station commands completed');
    const message = t('Auto sleep cancelled: {details}.', { details });
    this.setMessage(message);
    pushToast(message, success || failed ? 'warning' : 'info');
  }

  private setMessage(message: string) {
    this.dependencies.setStatusMessage(message);
  }
}
