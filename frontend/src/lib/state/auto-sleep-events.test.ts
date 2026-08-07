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
});
