import { writable } from 'svelte/store';

export interface ToastItem {
  id: number;
  text: string;
  kind: 'error' | 'warning' | 'info' | 'success';
}

export const toasts = writable<ToastItem[]>([]);

let nextId = 1;
const timers = new Map<number, ReturnType<typeof setTimeout>>();

export function pushToast(text: string, kind: ToastItem['kind'] = 'error', timeout = 6000) {
  const id = nextId++;
  toasts.update((list) => [...list, { id, text, kind }]);
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
