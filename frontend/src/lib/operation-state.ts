export type GlobalOperation = 'idle' | 'scanning' | 'status-refresh' | 'bulk-power';

export interface OperationState {
  global: GlobalOperation;
  externalScanning: boolean;
  gattAddresses: ReadonlySet<string>;
  configAddresses: ReadonlySet<string>;
}

export interface OperationLocks {
  scanLocked: boolean;
  bulkLocked: boolean;
  stationLocked: boolean;
  anyDeviceOperation: boolean;
}

export function deriveOperationLocks(state: OperationState): OperationLocks {
  const anyDeviceOperation = state.gattAddresses.size > 0 || state.configAddresses.size > 0;
  // A periodic status read is background work, not an exclusive user action.
  // Let users act on a station while it runs; the backend arbitrates any GATT
  // conflict without briefly disabling every card after a scan completes.
  const exclusiveGlobalOperation = state.global === 'scanning' || state.global === 'bulk-power';
  const bluetoothBusy = exclusiveGlobalOperation || state.externalScanning || anyDeviceOperation;
  return {
    scanLocked: bluetoothBusy,
    bulkLocked: bluetoothBusy,
    stationLocked: exclusiveGlobalOperation || state.externalScanning,
    anyDeviceOperation
  };
}
