import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearToasts, pushToast, toasts } from './toast';

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  clearToasts();
  vi.useRealTimers();
});

describe('pushToast', () => {
  it('keeps a single visible copy of a repeated toast and restarts its timer', () => {
    const id = pushToast('Power change failed', 'error');
    pushToast('Power change failed', 'error');
    expect(get(toasts)).toHaveLength(1);

    vi.advanceTimersByTime(4000);
    // The repeat restarted retention, so 4s + 4s is still within 6s of the
    // most recent push.
    pushToast('Power change failed', 'error');
    vi.advanceTimersByTime(5000);
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(2000);
    expect(get(toasts)).toHaveLength(0);
    expect(id).toBeTypeOf('number');
  });

  it('does not treat the same text with a different kind as a duplicate', () => {
    pushToast('Watch out', 'warning');
    pushToast('Watch out', 'info');
    expect(get(toasts)).toHaveLength(2);
  });

  it('evicts the oldest toast beyond four visible', () => {
    const first = pushToast('one');
    pushToast('two');
    pushToast('three');
    pushToast('four');
    pushToast('five');
    expect(get(toasts)).toHaveLength(4);
    expect(get(toasts).some((item) => item.id === first)).toBe(false);
    // The evicted entry's dismissal timer must never resurrect itself.
    vi.advanceTimersByTime(60000);
    expect(get(toasts)).toHaveLength(0);
  });

  it('retains short toasts for the minimum retention', () => {
    pushToast('short');
    vi.advanceTimersByTime(5999);
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(get(toasts)).toHaveLength(0);
  });

  it('gives long toasts extra reading time up to a cap', () => {
    const longText = 'x'.repeat(400);
    pushToast(longText);
    vi.advanceTimersByTime(14999);
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(get(toasts)).toHaveLength(0);
  });

  it('honours an explicit timeout', () => {
    pushToast('quick', 'info', 1000);
    vi.advanceTimersByTime(999);
    expect(get(toasts)).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(get(toasts)).toHaveLength(0);
  });
});

describe('clearToasts', () => {
  it('removes every toast and cancels pending dismissals', () => {
    pushToast('a');
    pushToast('b');
    clearToasts();
    expect(get(toasts)).toHaveLength(0);
    vi.advanceTimersByTime(60000);
    expect(get(toasts)).toHaveLength(0);
  });
});
