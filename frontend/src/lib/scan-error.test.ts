import { describe, expect, it } from 'vitest';
import { classifyScanError, scanErrorCopy } from './scan-error';

describe('classifyScanError', () => {
  it('classifies a disabled Bluetooth radio', () => {
    expect(classifyScanError('Bluetooth is unavailable; turn on Bluetooth and retry').kind)
      .toBe('bluetooth-off');
    expect(classifyScanError('bluetooth is off').kind).toBe('bluetooth-off');
    expect(classifyScanError('radio is off').kind).toBe('bluetooth-off');
  });

  it('classifies a missing adapter', () => {
    expect(classifyScanError('no bluetooth adapter found').kind).toBe('adapter-missing');
    expect(classifyScanError('adapter not present').kind).toBe('adapter-missing');
    expect(classifyScanError('device not found').kind).toBe('adapter-missing');
  });

  it('classifies permission failures', () => {
    expect(classifyScanError('permission denied').kind).toBe('permission');
    expect(classifyScanError('Access is denied.').kind).toBe('permission');
  });

  it('classifies timeouts', () => {
    expect(classifyScanError('operation timed out').kind).toBe('timeout');
    expect(classifyScanError('context deadline exceeded').kind).toBe('timeout');
  });

  it('falls back to unknown and preserves the detail text', () => {
    const info = classifyScanError(new Error('weird backend failure'));
    expect(info.kind).toBe('unknown');
    expect(info.detail).toBe('weird backend failure');
  });

  it('handles null and undefined errors', () => {
    expect(classifyScanError(null).kind).toBe('unknown');
    expect(classifyScanError(undefined).detail).toBe('');
  });
});

describe('scanErrorCopy', () => {
  it('gives every kind an actionable heading and steps', () => {
    const kinds = ['bluetooth-off', 'adapter-missing', 'permission', 'timeout', 'unknown'] as const;
    for (const kind of kinds) {
      const copy = scanErrorCopy({ kind, detail: 'detail' });
      expect(copy.heading).not.toBe('');
      expect(copy.explanation).not.toBe('');
      expect(copy.steps.length).toBeGreaterThanOrEqual(2);
    }
  });

  it('targets the Bluetooth troubleshooting message at the radio', () => {
    const copy = scanErrorCopy(classifyScanError('Bluetooth is unavailable'));
    expect(copy.heading).toBe('Bluetooth is unavailable');
    expect(copy.steps.join(' ')).toMatch(/Bluetooth/i);
  });
});
