import type { station } from '../../wailsjs/go/models';

export interface TerminalScanResult {
  state: string;
  found?: number;
  known?: number;
  error?: string;
  warnings?: string[];
  external?: boolean;
}

export function formatTerminalScanResult(result: TerminalScanResult): string {
  const prefix = result.external ? 'External scan' : 'Scan';
  const warnings = result.warnings?.filter(Boolean).join(' ') ?? '';

  switch (result.state) {
    case 'cancelled':
      return warnings ? `${prefix} stopped. ${warnings}` : `${prefix} stopped.`;
    case 'failed': {
      const err = result.error ? `: ${result.error}` : '';
      const base = `${prefix} failed${err}`;
      return warnings ? `${base} ${warnings}` : base;
    }
    default: {
      const found = result.found ?? 0;
      const known = result.known ?? 0;
      const summary = found
        ? `found ${found}; ${known} known station(s).`
        : 'no stations found in this scan.';
      const message = result.external
        ? `${prefix} completed: ${summary}`
        : `${summary[0].toUpperCase()}${summary.slice(1)}`;
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
  const counts = `${summary.confirmed} confirmed; ${summary.unconfirmed} sent but unconfirmed; ${summary.alreadyAtTarget} already at target; ${summary.skipped} skipped`;
  return summary.failed.length
    ? `${counts}; ${summary.failed.length} failed for ${target}: ${summary.failed.map((item) => `${item.name || item.address}: ${item.error || 'command failed'}`).join(' | ')}`
    : `${counts} for ${target}.`;
}
