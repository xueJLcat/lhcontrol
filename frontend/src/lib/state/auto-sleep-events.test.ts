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
      applyStations: vi.fn()
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
      applyStations: vi.fn()
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
      applyStations: vi.fn()
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
      applyStations: vi.fn()
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
});
