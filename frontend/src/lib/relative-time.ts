import { t } from './i18n.svelte';

export function relativeTime(iso: string, now: number = Date.now()): string {
  if (!iso) return '';
  const timestamp = Date.parse(iso);
  if (Number.isNaN(timestamp)) return iso;
  const elapsedSeconds = Math.floor((now - timestamp) / 1000);
  if (elapsedSeconds < -10) return t('clock mismatch');
  if (elapsedSeconds < 10) return t('just now');
  if (elapsedSeconds < 60) return t('{count}s ago', { count: elapsedSeconds });
  const minutes = Math.floor(elapsedSeconds / 60);
  if (minutes < 60) return t('{count}m ago', { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t('{count}h ago', { count: hours });
  return t('{count}d ago', { count: Math.floor(hours / 24) });
}
