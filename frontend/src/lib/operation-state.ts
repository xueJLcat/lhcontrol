export type GlobalOperation = 'idle' | 'scanning' | 'status-refresh' | 'bulk-power';

export interface OperationState {
  global: GlobalOperation;
  externalScanning: boolean;
  deviceAddresses: ReadonlySet<string>;
}

export interface OperationLocks {
  scanLocked: boolean;
  bulkLocked: boolean;
  stationLocked: boolean;
  anyDeviceOperation: boolean;
}

export function deriveOperationLocks(state: OperationState): OperationLocks {
  const anyDeviceOperation = state.deviceAddresses.size > 0;
  const globalBusy = state.global !== 'idle';
  const bluetoothBusy = globalBusy || state.externalScanning || anyDeviceOperation;
  return {
    scanLocked: bluetoothBusy,
    bulkLocked: bluetoothBusy,
    stationLocked: globalBusy || state.externalScanning,
    anyDeviceOperation
  };
}
