import { describe, expect, it } from 'vitest';
import { deriveOperationLocks, type GlobalOperation } from './operation-state';

function locks(
  global: GlobalOperation,
  externalScanning = false,
  gattDevices: string[] = [],
  configDevices: string[] = []
) {
  return deriveOperationLocks({
    global,
    externalScanning,
    gattAddresses: new Set(gattDevices),
    configAddresses: new Set(configDevices)
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

  it.each<GlobalOperation>(['scanning', 'bulk-power'])(
    'locks all Bluetooth entry points during %s',
    (operation) => {
      expect(locks(operation)).toMatchObject({
        scanLocked: true,
        bulkLocked: true,
        stationLocked: true
      });
    }
  );

  it('keeps station controls available during a background status refresh', () => {
    expect(locks('status-refresh')).toEqual({
      scanLocked: false,
      bulkLocked: false,
      stationLocked: false,
      anyDeviceOperation: false
    });
  });

  it('locks scan and bulk while an individual device is active', () => {
    expect(locks('idle', false, ['AA'])).toEqual({
      scanLocked: true,
      bulkLocked: true,
      stationLocked: false,
      anyDeviceOperation: true
    });
  });

  it('locks scan and bulk while a configuration write is active without locking GATT entry points', () => {
    expect(locks('idle', false, [], ['AA'])).toEqual({
      scanLocked: true,
      bulkLocked: true,
      stationLocked: false,
      anyDeviceOperation: true
    });
  });
});
