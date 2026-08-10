import type { station } from '../../wailsjs/go/models';
import { locale, t } from './i18n.svelte';

export interface TerminalScanResult {
  state: string;
  found?: number;
  known?: number;
  error?: string;
  warnings?: string[];
  external?: boolean;
}

// Only terminal states describe a finished scan. GetScanStatus reads racing a
// newly started scan can return its starting/running state; consumers must
// skip treating such a snapshot as the finished scan's outcome.
export function isTerminalScanState(state: string | null | undefined): boolean {
  return state === 'completed' || state === 'failed' || state === 'cancelled';
}

export function formatTerminalScanResult(result: TerminalScanResult): string {
  const prefix = t(result.external ? 'External scan' : 'Scan');
  const warnings = result.warnings?.filter(Boolean).join(' ') ?? '';

  switch (result.state) {
    case 'cancelled':
      return `${t('{prefix} stopped.', { prefix })}${warnings ? ` ${warnings}` : ''}`;
    case 'failed': {
      const err = result.error ? `: ${result.error}` : '';
      const base = `${t('{prefix} failed', { prefix })}${err}`;
      return warnings ? `${base} ${warnings}` : base;
    }
    default: {
      const found = result.found ?? 0;
      const known = result.known ?? 0;
      const knownLabel = t(known === 1 ? '{count} known station.' : '{count} known stations.', { count: known });
      const summary = found
        ? t('found {found}; {knownLabel}', { found, knownLabel })
        : t('no stations found in this scan.');
      const message = result.external
        ? t('{prefix} completed: {summary}', { prefix, summary })
        : locale() === 'en' ? `${summary[0].toUpperCase()}${summary.slice(1)}` : summary;
      return warnings ? `${message} ${warnings}` : message;
    }
  }
}

export interface BulkResultSummary {
  confirmed: number;
  unconfirmed: number;
  alreadyAtTarget: number;
  skipped: number;
  failed: station.BulkPowerStationResult[];
}

export function summarizeBulkResult(results: station.BulkPowerStationResult[]): BulkResultSummary {
  const failed = results.filter((item) => !item.success && !item.skipped);
  return {
    confirmed: results.filter((item) => item.success && !item.skipped && item.confirmed).length,
    unconfirmed: results.filter((item) => item.success && !item.skipped && !item.confirmed).length,
    alreadyAtTarget: results.filter((item) => item.skipped && item.success && item.confirmed).length,
    skipped: results.filter((item) => item.skipped && !(item.success && item.confirmed)).length,
    failed
  };
}

export function formatBulkResult(target: string, summary: BulkResultSummary): string {
  const counts = t('{confirmed} confirmed; {unconfirmed} sent but unconfirmed; {already} already at target; {skipped} skipped', {
    confirmed: summary.confirmed,
    unconfirmed: summary.unconfirmed,
    already: summary.alreadyAtTarget,
    skipped: summary.skipped
  });
  return summary.failed.length
    ? `${t('{counts}; {failed} failed for {target}', { counts, failed: summary.failed.length, target })}: ${summary.failed.map((item) => `${item.name || item.address}: ${item.error || t('command failed')}`).join(' | ')}`
    : t('{counts} for {target}.', { counts, target });
}
