import { writable } from 'svelte/store';

export interface ToastItem {
  id: number;
  text: string;
  kind: 'error' | 'info' | 'success';
}

export const toasts = writable<ToastItem[]>([]);

let nextId = 1;

export function pushToast(text: string, kind: 'error' | 'info' | 'success' = 'error', timeout = 6000) {
  const id = nextId++;
  toasts.update((list) => [...list, { id, text, kind }]);
  setTimeout(() => {
    toasts.update((list) => list.filter((item) => item.id !== id));
  }, timeout);
}

export function dismissToast(id: number) {
  toasts.update((list) => list.filter((item) => item.id !== id));
}
