import { afterEach, describe, expect, it, vi } from 'vitest';
import { dur, prefersReducedMotion } from './motion';

type MatchMediaListener = (event: { matches: boolean }) => void;

function stubMatchMedia(matches: boolean) {
  let listener: MatchMediaListener | null = null;
  const query = {
    matches,
    media: '(prefers-reduced-motion: reduce)',
    addEventListener: (_name: string, callback: MatchMediaListener) => {
      listener = callback;
    },
    removeEventListener: () => {
      listener = null;
    }
  };
  vi.stubGlobal('matchMedia', vi.fn(() => query));
  return {
    setMatches(value: boolean) {
      query.matches = value;
      listener?.({ matches: value });
    }
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('motion helpers', () => {
  it('keeps transition durations untouched when motion is allowed', async () => {
    stubMatchMedia(false);
    vi.resetModules();
    const fresh = await import('./motion');
    expect(fresh.prefersReducedMotion()).toBe(false);
    expect(fresh.dur({ duration: 200 })).toEqual({ duration: 200 });
  });

  it('zeroes transition durations when the OS prefers reduced motion', async () => {
    stubMatchMedia(true);
    vi.resetModules();
    const fresh = await import('./motion');
    expect(fresh.prefersReducedMotion()).toBe(true);
    expect(fresh.dur({ duration: 200, delay: 60 })).toEqual({ duration: 0, delay: 60 });
  });

  it('tracks runtime preference changes', async () => {
    const control = stubMatchMedia(false);
    vi.resetModules();
    const fresh = await import('./motion');
    expect(fresh.prefersReducedMotion()).toBe(false);
    control.setMatches(true);
    expect(fresh.prefersReducedMotion()).toBe(true);
  });
});
