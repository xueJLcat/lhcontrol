let reduced = false;

if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
  const query = window.matchMedia('(prefers-reduced-motion: reduce)');
  reduced = query.matches;
  // Older WebViews expose addListener only; guard the modern API first.
  if (typeof query.addEventListener === 'function') {
    query.addEventListener('change', (event) => {
      reduced = event.matches;
    });
  }
}

export function prefersReducedMotion(): boolean {
  return reduced;
}

// Svelte transitions are JavaScript-driven and ignore the CSS reduced-motion
// override, so every transition params object is routed through here to give
// OS-level motion preferences a global kill switch.
export function dur<T extends { duration?: number }>(params: T): T {
  if (!reduced) return params;
  return { ...params, duration: 0 };
}
