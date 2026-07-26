import { describe, expect, it } from 'vitest';
import { deriveOperationLocks, type GlobalOperation } from './operation-state';

function locks(global: GlobalOperation, externalScanning = false, devices: string[] = []) {
  return deriveOperationLocks({
    global,
    externalScanning,
    deviceAddresses: new Set(devices)
  });
}

describe('deriveOperationLocks', () => {
  it('allows controls while idle', () => {
    expect(locks('idle')).toEqual({
      scanLocked: false,
      bulkLocked: false,
      stationLocked: false,
      anyDeviceOperation: false
    });
  });

  it.each<GlobalOperation>(['scanning', 'status-refresh', 'bulk-power'])(
    'locks all Bluetooth entry points during %s',
    (operation) => {
      expect(locks(operation)).toMatchObject({
        scanLocked: true,
        bulkLocked: true,
        stationLocked: true
      });
    }
  );

  it('locks scan and bulk while an individual device is active', () => {
    expect(locks('idle', false, ['AA'])).toEqual({
      scanLocked: true,
      bulkLocked: true,
      stationLocked: false,
      anyDeviceOperation: true
    });
  });
});
