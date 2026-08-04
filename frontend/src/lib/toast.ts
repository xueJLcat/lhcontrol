import { writable } from 'svelte/store';

export interface ToastItem {
  id: number;
  text: string;
  kind: 'error' | 'warning' | 'info' | 'success';
}

export const toasts = writable<ToastItem[]>([]);

const MAX_TOASTS = 4;

let nextId = 1;
const timers = new Map<number, ReturnType<typeof setTimeout>>();

export function pushToast(text: string, kind: ToastItem['kind'] = 'error', timeout = 6000) {
  const id = nextId++;
  let evicted: number[] = [];
  toasts.update((list) => {
    const next = [...list, { id, kind, text }];
    const overflow = next.length - MAX_TOASTS;
    if (overflow <= 0) return next;
    evicted = next.slice(0, overflow).map((item) => item.id);
    return next.slice(overflow);
  });
  // Clear the dismissal timers of evicted toasts so they cannot fire
  // against stale store entries later.
  for (const evictedId of evicted) {
    const timer = timers.get(evictedId);
    if (timer) clearTimeout(timer);
    timers.delete(evictedId);
  }
  timers.set(id, setTimeout(() => {
    timers.delete(id);
    toasts.update((list) => list.filter((item) => item.id !== id));
  }, timeout));
}

export function dismissToast(id: number) {
  const timer = timers.get(id);
  if (timer) clearTimeout(timer);
  timers.delete(id);
  toasts.update((list) => list.filter((item) => item.id !== id));
}

export function clearToasts() {
  for (const timer of timers.values()) clearTimeout(timer);
  timers.clear();
  toasts.set([]);
}
