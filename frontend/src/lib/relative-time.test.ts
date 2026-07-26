import { describe, expect, it } from 'vitest';
import { relativeTime } from './relative-time';

const now = Date.parse('2026-07-26T12:00:00Z');

describe('relativeTime', () => {
  it('returns empty string for empty input', () => {
    expect(relativeTime('', now)).toBe('');
  });

  it('returns the input unchanged when it cannot be parsed', () => {
    expect(relativeTime('not-a-date', now)).toBe('not-a-date');
  });

  it('reports "just now" under 10 seconds', () => {
    expect(relativeTime('2026-07-26T11:59:51Z', now)).toBe('just now');
  });

  it('reports "just now" for future timestamps (clock skew)', () => {
    expect(relativeTime('2026-07-26T12:00:05Z', now)).toBe('just now');
  });

  it('reports seconds below one minute', () => {
    expect(relativeTime('2026-07-26T11:59:15Z', now)).toBe('45s ago');
  });

  it('reports minutes below one hour', () => {
    expect(relativeTime('2026-07-26T11:57:00Z', now)).toBe('3m ago');
  });

  it('reports hours below one day', () => {
    expect(relativeTime('2026-07-26T10:00:00Z', now)).toBe('2h ago');
  });

  it('reports days beyond 24 hours', () => {
    expect(relativeTime('2026-07-24T12:00:00Z', now)).toBe('2d ago');
  });
});
