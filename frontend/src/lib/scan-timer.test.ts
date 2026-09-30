import { describe, expect, it, vi } from 'vitest';
import { ScanTimer } from './scan-timer';

describe('ScanTimer', () => {
  it('keeps counting elapsed time across a system clock rollback', () => {
    vi.useFakeTimers();
    const elapsed: number[] = [];
    const timer = new ScanTimer((seconds) => elapsed.push(seconds));
    try {
      vi.setSystemTime(new Date('2026-09-23T10:00:00Z'));
      timer.begin();
      vi.advanceTimersByTime(3_000);
      vi.setSystemTime(new Date('2026-09-23T09:00:00Z'));
      vi.advanceTimersByTime(2_000);
      expect(elapsed.at(-1)).toBe(5);
    } finally {
      timer.dispose();
      vi.useRealTimers();
    }
  });
});
