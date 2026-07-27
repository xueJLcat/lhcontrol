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
  // A status refresh can occupy both backend GATT slots. Locking controls for
  // its short duration prevents enabled actions from failing immediately as busy.
  const exclusiveGlobalOperation = state.global === 'scanning' ||
    state.global === 'status-refresh' || state.global === 'bulk-power';
  const bluetoothBusy = exclusiveGlobalOperation || state.externalScanning || anyDeviceOperation;
  return {
    scanLocked: bluetoothBusy,
    bulkLocked: bluetoothBusy,
    stationLocked: exclusiveGlobalOperation || state.externalScanning,
    anyDeviceOperation
  };
}
