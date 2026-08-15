import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../toast', () => ({ pushToast: vi.fn() }));

import { pushToast } from '../toast';
import { setLanguagePreference } from '../i18n.svelte';
import { AutoSleepEventCoordinator } from './auto-sleep-events';

describe('AutoSleepEventCoordinator', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setLanguagePreference('en');
  });

  it('reports partial results when automatic sleep times out', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => false)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({
      phase: 'timed-out',
      timedOut: true,
      success: 2,
      unconfirmed: 1,
      failed: 1,
      timedOutSkipped: 3
    });

    const message = 'Auto sleep timed out: 2 confirmed, 1 unconfirmed, 1 failed, 3 skipped due to timeout.';
    expect(dependencies.setRunning).toHaveBeenCalledWith(false);
    expect(dependencies.beginStatusOperation).toHaveBeenCalledOnce();
    expect(dependencies.setStatusMessage).toHaveBeenCalledWith(message);
    expect(pushToast).toHaveBeenCalledWith(message, 'warning');
  });

  it('keeps an older draining action locked without letting its terminal event overwrite a replacement', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => false)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({ id: 1, phase: 'started' });
    coordinator.handle({ id: 2, phase: 'started' });
    vi.clearAllMocks();

    coordinator.handle({ id: 1, phase: 'cancelled', error: 'superseded', updateId: 7, stations: [] });

    expect(dependencies.applyStations).toHaveBeenCalledWith(7, []);
    expect(dependencies.setRunning).toHaveBeenLastCalledWith(true);
    expect(dependencies.setStatusMessage).not.toHaveBeenCalled();
    expect(pushToast).not.toHaveBeenCalled();

    coordinator.handle({ id: 2, phase: 'completed', success: 1, updateId: 8, stations: [] });

    expect(dependencies.setRunning).toHaveBeenLastCalledWith(false);
    expect(dependencies.setStatusMessage).toHaveBeenLastCalledWith(
      'Auto sleep finished: 1 confirmed, 0 unconfirmed, 0 failed, 0 skipped.'
    );
  });

  it('ignores a delayed start after the same action already reached a terminal phase', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => false)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({ id: 7, phase: 'completed', success: 1, updateId: 12, stations: [] });
    expect(dependencies.setRunning).toHaveBeenLastCalledWith(false);

    vi.clearAllMocks();
    coordinator.handle({ id: 7, phase: 'started' });

    expect(dependencies.setRunning).not.toHaveBeenCalled();
    expect(dependencies.beginStatusOperation).not.toHaveBeenCalled();
    expect(dependencies.setStatusMessage).not.toHaveBeenCalled();
    expect(pushToast).not.toHaveBeenCalled();
  });

  it('does not replay status or notifications for a duplicate terminal event', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => false)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);
    const event = { id: 9, phase: 'failed' as const, error: 'adapter unavailable' };

    coordinator.handle(event);
    expect(dependencies.setStatusMessage).toHaveBeenCalledOnce();
    expect(pushToast).toHaveBeenCalledOnce();

    vi.clearAllMocks();
    coordinator.handle(event);

    expect(dependencies.beginStatusOperation).not.toHaveBeenCalled();
    expect(dependencies.setStatusMessage).not.toHaveBeenCalled();
    expect(pushToast).not.toHaveBeenCalled();
  });

  it('treats a cancelled sleep with only skipped stations as a warning', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => false)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({ id: 1, phase: 'started' });
    vi.clearAllMocks();
    coordinator.handle({ id: 1, phase: 'cancelled', skipped: 2, updateId: 3, stations: [] });

    expect(pushToast).toHaveBeenCalledWith(
      'Auto sleep cancelled: 0 confirmed, 0 unconfirmed, 0 failed, 2 skipped.',
      'warning'
    );
  });

  it('does not let an unsequenced terminal event unlock a tracked action', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => false)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    // A sequenced action starts and is still tracked as active.
    coordinator.handle({ id: 5, phase: 'started' });
    expect(dependencies.setRunning).toHaveBeenLastCalledWith(true);
    vi.clearAllMocks();

    // An unsequenced (legacy) terminal event must not clear the running flag
    // while the tracked action is still draining.
    coordinator.handle({ phase: 'completed', success: 1, updateId: 6, stations: [] });

    expect(dependencies.setRunning).not.toHaveBeenCalledWith(false);
  });

  it('forgets tracked actions when the authoritative operation snapshot is idle', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => false)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({ id: 1, phase: 'started' });
    coordinator.reconcileIdle();
    expect(dependencies.setRunning).toHaveBeenLastCalledWith(false);

    vi.clearAllMocks();
    coordinator.handle({ id: 2, phase: 'started' });
    coordinator.handle({ id: 2, phase: 'completed', success: 1 });

    expect(dependencies.setRunning).toHaveBeenLastCalledWith(false);
  });

  it('keeps a foreground operation status line when auto sleep skips while it runs', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => true)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({ id: 3, phase: 'skipped', error: 'another Bluetooth operation is in progress' });

    // The skip is produced exactly because the foreground operation holds the
    // backend busy; its completion summary must win the status line. The toast
    // still reports the skipped auto sleep.
    expect(dependencies.beginStatusOperation).not.toHaveBeenCalled();
    expect(dependencies.setStatusMessage).not.toHaveBeenCalled();
    expect(pushToast).toHaveBeenCalledWith(
      'Auto sleep skipped: another Bluetooth operation is in progress.',
      'info'
    );
  });

  it('keeps a foreground operation status line for the started phase as well', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => true)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({ id: 6, phase: 'started' });

    // The started event is emitted before the action holds the operation
    // lock, so an interactive scan/bulk can own the line; its summary must
    // survive. The toast still reports the trigger.
    expect(dependencies.setRunning).toHaveBeenCalledWith(true);
    expect(dependencies.beginStatusOperation).not.toHaveBeenCalled();
    expect(dependencies.setStatusMessage).not.toHaveBeenCalled();
    expect(pushToast).toHaveBeenCalledWith(
      'Session ended — scanning and putting all stations to sleep.',
      'info'
    );
  });

  it('keeps a foreground operation status line for terminal outcomes as well', () => {
    const dependencies = {
      isDisposed: vi.fn(() => false),
      setRunning: vi.fn(),
      beginStatusOperation: vi.fn(),
      setStatusMessage: vi.fn(),
      applyStations: vi.fn(),
      foregroundOwnsStatusLine: vi.fn(() => true)
    };
    const coordinator = new AutoSleepEventCoordinator(dependencies);

    coordinator.handle({ id: 4, phase: 'completed', success: 2 });

    expect(dependencies.beginStatusOperation).not.toHaveBeenCalled();
    expect(dependencies.setStatusMessage).not.toHaveBeenCalled();
    expect(pushToast).toHaveBeenCalledWith(
      'Auto sleep finished: 2 station(s) put to sleep.',
      'success'
    );
  });
});
