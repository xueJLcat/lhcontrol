export type ScanErrorKind =
  | 'bluetooth-off'
  | 'adapter-missing'
  | 'permission'
  | 'timeout'
  | 'unknown';

export interface ScanErrorInfo {
  kind: ScanErrorKind;
  detail: string;
}

export interface ScanErrorCopy {
  heading: string;
  explanation: string;
  steps: string[];
}

// Backend errors are free-form Go strings; classification is keyword based so
// the recovery card can offer targeted troubleshooting without guessing.
const PATTERNS: Array<[ScanErrorKind, RegExp]> = [
  ['bluetooth-off', /bluetooth is (off|disabled|unavailable|not enabled|not ready)|radio (is )?(off|disabled|unavailable)|turn on bluetooth/i],
  ['adapter-missing', /no (bluetooth |bt )?(radio )?adapter|adapter (is )?(missing|not found|unavailable)|no such device|device (was )?not found|adapter not present/i],
  ['permission', /permission denied|access (is )?denied|unauthorized|insufficient privileges|not authorized/i],
  ['timeout', /timed? ?out|deadline exceeded|context deadline/i]
];

export function classifyScanError(error: unknown): ScanErrorInfo {
  const detail = (error instanceof Error ? error.message : String(error ?? '')).trim();
  for (const [kind, pattern] of PATTERNS) {
    if (pattern.test(detail)) return { kind, detail };
  }
  return { kind: 'unknown', detail };
}

export function scanErrorCopy(info: ScanErrorInfo): ScanErrorCopy {
  switch (info.kind) {
    case 'bluetooth-off':
      return {
        heading: 'Bluetooth is unavailable',
        explanation: 'The scan could not run because the Bluetooth radio is off or unavailable.',
        steps: [
          'Open Windows Settings → Bluetooth & devices and turn Bluetooth on.',
          'Wait a moment for the adapter to become ready, then retry the scan.'
        ]
      };
    case 'adapter-missing':
      return {
        heading: 'No Bluetooth adapter found',
        explanation: 'The scan could not run because no Bluetooth adapter is present.',
        steps: [
          'Plug in or re-enable the Bluetooth adapter.',
          'Check Device Manager for a disabled or missing adapter, then retry the scan.'
        ]
      };
    case 'permission':
      return {
        heading: 'Bluetooth access was denied',
        explanation: 'The scan could not run because this app is not allowed to use Bluetooth.',
        steps: [
          'Grant Bluetooth permission to the app (or run it with the required rights).',
          'Retry the scan once access is allowed.'
        ]
      };
    case 'timeout':
      return {
        heading: 'The scan timed out',
        explanation: 'The scan did not finish in time; the adapter may be busy or a station may be out of range.',
        steps: [
          'Move closer to the base stations and keep the adapter unobstructed.',
          'Retry the scan; repeated timeouts may indicate an adapter problem.'
        ]
      };
    default:
      return {
        heading: 'Scan failed',
        explanation: 'The scan could not be completed.',
        steps: [
          'Check that the Bluetooth adapter is connected and the base stations are powered.',
          'Retry the scan; the error details below are preserved for diagnostics.'
        ]
      };
  }
}
