import type { main, station } from '../../wailsjs/go/models';
import type { PowerTarget } from './types';
import * as bindings from '../../wailsjs/go/main/App';

// Wails bindings dereference window.go synchronously; outside the WebView
// (or before the runtime is ready) that throws before any .catch() can attach.
// Wrapping every call turns a synchronous throw into a rejected promise so the
// normal error paths handle it.
function call<T>(invoke: () => T): Promise<Awaited<T>> {
  try {
    return Promise.resolve(invoke());
  } catch (error) {
    return Promise.reject(error instanceof Error ? error : new Error(String(error)));
  }
}

export function CheckAllStationStatuses(): Promise<station.StationInfo[]> {
  return call(() => bindings.CheckAllStationStatuses());
}

export function GetAPIStatus(): Promise<main.APIStatus> {
  return call(() => bindings.GetAPIStatus());
}

export function GetCurrentStationInfo(): Promise<station.StationInfo[]> {
  return call(() => bindings.GetCurrentStationInfo());
}

export function GetScanStatus(): Promise<station.ScanStatus> {
  return call(() => bindings.GetScanStatus());
}

export function IdentifyStation(address: string): Promise<void> {
  return call(() => bindings.IdentifyStation(address));
}

export function IsScanning(): Promise<boolean> {
  return call(() => bindings.IsScanning());
}

export function RefreshStationCapabilities(address: string): Promise<station.StationInfo> {
  return call(() => bindings.RefreshStationCapabilities(address));
}

export function RenameStationByAddress(address: string, name: string): Promise<void> {
  return call(() => bindings.RenameStationByAddress(address, name));
}

export function ScanAndFetchStations(): Promise<station.StationInfo[]> {
  return call(() => bindings.ScanAndFetchStations());
}

export function SetAllStationsPowerDetailed(target: PowerTarget): Promise<station.BulkPowerResult> {
  return call(() => bindings.SetAllStationsPowerDetailed(target));
}

export function SetStationChannel(address: string, channel: number, allowUnknownConflictRisk: boolean): Promise<station.ChannelChangeResult> {
  return call(() => bindings.SetStationChannel(address, channel, allowUnknownConflictRisk));
}

export function SetStationPower(address: string, target: PowerTarget): Promise<station.PowerActionResult> {
  return call(() => bindings.SetStationPower(address, target));
}

export function StopScan(): Promise<void> {
  return call(() => bindings.StopScan());
}
