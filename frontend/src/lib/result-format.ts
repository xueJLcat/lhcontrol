import type { station } from '../../wailsjs/go/models';

interface ScanResult {
  found: number;
  warnings?: string[];
}

export function formatScanResult(result: ScanResult, known: number, external = false): string {
  const summary = result.found
    ? `found ${result.found}; ${known} known station(s).`
    : 'no stations found in this scan.';
  const warnings = result.warnings?.filter(Boolean).join(' ') ?? '';
  const message = external ? `External scan completed: ${summary}` : `${summary[0].toUpperCase()}${summary.slice(1)}`;
  return warnings ? `${message} ${warnings}` : message;
}

export interface BulkResultSummary {
  confirmed: number;
  unconfirmed: number;
  skipped: number;
  failed: station.BulkPowerStationResult[];
}

export function summarizeBulkResult(results: station.BulkPowerStationResult[]): BulkResultSummary {
  const failed = results.filter((item) => !item.success && !item.skipped);
  return {
    confirmed: results.filter((item) => item.success && !item.skipped && item.confirmed).length,
    unconfirmed: results.filter((item) => item.success && !item.skipped && !item.confirmed).length,
    skipped: results.filter((item) => item.skipped).length,
    failed
  };
}

export function formatBulkResult(target: string, summary: BulkResultSummary): string {
  const counts = `${summary.confirmed} confirmed; ${summary.unconfirmed} sent but unconfirmed; ${summary.skipped} skipped`;
  return summary.failed.length
    ? `${counts}; ${summary.failed.length} failed for ${target}: ${summary.failed.map((item) => `${item.name || item.address}: ${item.error || 'command failed'}`).join(' | ')}`
    : `${counts} for ${target}.`;
}
