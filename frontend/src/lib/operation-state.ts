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
  const exclusiveGlobalOperation = state.global === 'scanning' ||
    state.global === 'bulk-power';
  const globalActionBusy = state.global !== 'idle';
  const bluetoothBusy = globalActionBusy || state.externalScanning || anyDeviceOperation;
  return {
    scanLocked: bluetoothBusy,
    bulkLocked: bluetoothBusy,
    stationLocked: exclusiveGlobalOperation || state.externalScanning,
    anyDeviceOperation
  };
}
