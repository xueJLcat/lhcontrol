import { writable } from 'svelte/store';

export interface ToastItem {
  id: number;
  text: string;
  kind: 'error' | 'warning' | 'info' | 'success';
}

export const toasts = writable<ToastItem[]>([]);

const MAX_TOASTS = 4;
const MIN_RETENTION_MS = 6000;
const MAX_RETENTION_MS = 15000;

// Long messages (bulk power reports can list every failed station) need
// more reading time than a short one-liner.
function retentionFor(text: string): number {
  return Math.min(MAX_RETENTION_MS, Math.max(MIN_RETENTION_MS, 4000 + text.length * 45));
}

let nextId = 1;
const timers = new Map<number, ReturnType<typeof setTimeout>>();

function scheduleDismiss(id: number, timeout: number) {
  timers.set(id, setTimeout(() => {
    timers.delete(id);
    toasts.update((list) => list.filter((item) => item.id !== id));
  }, timeout));
}

function dropTimers(ids: number[]) {
  for (const id of ids) {
    const timer = timers.get(id);
    if (timer) clearTimeout(timer);
    timers.delete(id);
  }
}

export function pushToast(text: string, kind: ToastItem['kind'] = 'error', timeout?: number): number {
  const retention = timeout ?? retentionFor(text);
  let duplicateId: number | null = null;
  let evicted: number[] = [];
  toasts.update((list) => {
    // An identical toast already visible keeps its slot and gets its
    // retention restarted instead of stacking copies that would evict
    // older, still-relevant notes.
    const existing = list.find((item) => item.text === text && item.kind === kind);
    if (existing) {
      duplicateId = existing.id;
      return list;
    }
    const next = [...list, { id: nextId++, kind, text }];
    const overflow = next.length - MAX_TOASTS;
    if (overflow <= 0) return next;
    evicted = next.slice(0, overflow).map((item) => item.id);
    return next.slice(overflow);
  });
  if (duplicateId !== null) {
    const timer = timers.get(duplicateId);
    if (timer) clearTimeout(timer);
    scheduleDismiss(duplicateId, retention);
    return duplicateId;
  }
  const id = nextId - 1;
  // Clear the dismissal timers of evicted toasts so they cannot fire
  // against stale store entries later.
  dropTimers(evicted);
  scheduleDismiss(id, retention);
  return id;
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
