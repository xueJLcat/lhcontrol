export function relativeTime(iso: string, now: number = Date.now()): string {
  if (!iso) return '';
  const timestamp = Date.parse(iso);
  if (Number.isNaN(timestamp)) return iso;
  const elapsedSeconds = Math.floor((now - timestamp) / 1000);
  if (elapsedSeconds < 10) return 'just now';
  if (elapsedSeconds < 60) return `${elapsedSeconds}s ago`;
  const minutes = Math.floor(elapsedSeconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}
