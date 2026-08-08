import type { autosleep, bluetooth, main, station } from '../../wailsjs/go/models';
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

export function CancelBulkPower(): Promise<void> {
  return call(() => bindings.CancelBulkPower());
}

export function GetAPIStatus(): Promise<main.APIStatus> {
  return call(() => bindings.GetAPIStatus());
}

export function GetAbsentStationRetryLimit(): Promise<number> {
  return call(() => bindings.GetAbsentStationRetryLimit());
}

export function GetAPIListenAddress(): Promise<string> {
  return call(() => bindings.GetAPIListenAddress());
}

export function GetAutoSleepSettings(): Promise<autosleep.Settings> {
  return call(() => bindings.GetAutoSleepSettings());
}

export function GetBluetoothInitRetrySeconds(): Promise<number> {
  return call(() => bindings.GetBluetoothInitRetrySeconds());
}

export function GetChannelConfirmAttempts(): Promise<number> {
  return call(() => bindings.GetChannelConfirmAttempts());
}

export function GetChannelConfirmIntervalMs(): Promise<number> {
  return call(() => bindings.GetChannelConfirmIntervalMs());
}

export function GetChannelScanFreshnessSeconds(): Promise<number> {
  return call(() => bindings.GetChannelScanFreshnessSeconds());
}

export function GetConfirmReconnectDelayMs(): Promise<number> {
  return call(() => bindings.GetConfirmReconnectDelayMs());
}

export function GetConfirmReconnectThreshold(): Promise<number> {
  return call(() => bindings.GetConfirmReconnectThreshold());
}

export function GetBootFallbackSeconds(): Promise<number> {
  return call(() => bindings.GetBootFallbackSeconds());
}

export function GetBulkPowerTimeoutSeconds(): Promise<number> {
  return call(() => bindings.GetBulkPowerTimeoutSeconds());
}

export function GetLanguage(): Promise<string> {
  return call(() => bindings.GetLanguage());
}

export function GetDiscoveryAttempts(): Promise<number> {
  return call(() => bindings.GetDiscoveryAttempts());
}

export function GetDiscoveryRetryDelayMs(): Promise<number> {
  return call(() => bindings.GetDiscoveryRetryDelayMs());
}

export function GetIdentifyAttempts(): Promise<number> {
  return call(() => bindings.GetIdentifyAttempts());
}

export function GetInitialReadTimeoutSeconds(): Promise<number> {
  return call(() => bindings.GetInitialReadTimeoutSeconds());
}

export function GetOperationRetryDelayMs(): Promise<number> {
  return call(() => bindings.GetOperationRetryDelayMs());
}

export function GetPresenceMissThreshold(): Promise<number> {
  return call(() => bindings.GetPresenceMissThreshold());
}

export function GetPowerConfirmAttemptsOff(): Promise<number> {
  return call(() => bindings.GetPowerConfirmAttemptsOff());
}

export function GetPowerConfirmAttemptsOn(): Promise<number> {
  return call(() => bindings.GetPowerConfirmAttemptsOn());
}

export function GetPowerConfirmPollIntervalMs(): Promise<number> {
  return call(() => bindings.GetPowerConfirmPollIntervalMs());
}

export function GetPowerWriteAttempts(): Promise<number> {
  return call(() => bindings.GetPowerWriteAttempts());
}

export function GetRecoveryRetryBaseSeconds(): Promise<number> {
  return call(() => bindings.GetRecoveryRetryBaseSeconds());
}

export function GetRecoveryRetryMaxSeconds(): Promise<number> {
  return call(() => bindings.GetRecoveryRetryMaxSeconds());
}

export function GetScanReadPhaseTimeoutSeconds(): Promise<number> {
  return call(() => bindings.GetScanReadPhaseTimeoutSeconds());
}

export function GetSleepFinalWriteTimeoutSeconds(): Promise<number> {
  return call(() => bindings.GetSleepFinalWriteTimeoutSeconds());
}

export function GetSleepPrepareGapMs(): Promise<number> {
  return call(() => bindings.GetSleepPrepareGapMs());
}

export function GetStationOperationTimeoutSeconds(): Promise<number> {
  return call(() => bindings.GetStationOperationTimeoutSeconds());
}

export function GetStatusReadTimeoutSeconds(): Promise<number> {
  return call(() => bindings.GetStatusReadTimeoutSeconds());
}

export function GetStatusRefreshTimeoutSeconds(): Promise<number> {
  return call(() => bindings.GetStatusRefreshTimeoutSeconds());
}

export function GetScanDurationSeconds(): Promise<number> {
  return call(() => bindings.GetScanDurationSeconds());
}

export function GetScanOnStartup(): Promise<boolean> {
  return call(() => bindings.GetScanOnStartup());
}

export function GetStatusPollIntervalSeconds(): Promise<number> {
  return call(() => bindings.GetStatusPollIntervalSeconds());
}

export function GetStatusPollingEnabled(): Promise<boolean> {
  return call(() => bindings.GetStatusPollingEnabled());
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

export function ListBluetoothAdapters(): Promise<bluetooth.AdapterInfo[]> {
  return call(() => bindings.ListBluetoothAdapters());
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

export function SetAbsentStationRetryLimit(limit: number): Promise<void> {
  return call(() => bindings.SetAbsentStationRetryLimit(limit));
}

export function SetAPIListenAddress(address: string): Promise<void> {
  return call(() => bindings.SetAPIListenAddress(address));
}

export function SetAutoSleepSettings(settings: autosleep.Settings): Promise<void> {
  return call(() => bindings.SetAutoSleepSettings(settings));
}

export function SetBluetoothInitRetrySeconds(retrySeconds: number): Promise<void> {
  return call(() => bindings.SetBluetoothInitRetrySeconds(retrySeconds));
}

export function SetBootFallbackSeconds(fallbackSeconds: number): Promise<void> {
  return call(() => bindings.SetBootFallbackSeconds(fallbackSeconds));
}

export function SetChannelConfirmAttempts(attempts: number): Promise<void> {
  return call(() => bindings.SetChannelConfirmAttempts(attempts));
}

export function SetChannelConfirmIntervalMs(intervalMs: number): Promise<void> {
  return call(() => bindings.SetChannelConfirmIntervalMs(intervalMs));
}

export function SetChannelScanFreshnessSeconds(freshnessSeconds: number): Promise<void> {
  return call(() => bindings.SetChannelScanFreshnessSeconds(freshnessSeconds));
}

export function SetConfirmReconnectDelayMs(delayMs: number): Promise<void> {
  return call(() => bindings.SetConfirmReconnectDelayMs(delayMs));
}

export function SetConfirmReconnectThreshold(threshold: number): Promise<void> {
  return call(() => bindings.SetConfirmReconnectThreshold(threshold));
}

export function SetBulkPowerTimeoutSeconds(timeoutSeconds: number): Promise<void> {
  return call(() => bindings.SetBulkPowerTimeoutSeconds(timeoutSeconds));
}

export function SetLanguage(language: string): Promise<void> {
  return call(() => bindings.SetLanguage(language));
}

export function SetPowerConfirmAttemptsOff(attempts: number): Promise<void> {
  return call(() => bindings.SetPowerConfirmAttemptsOff(attempts));
}

export function SetPowerConfirmAttemptsOn(attempts: number): Promise<void> {
  return call(() => bindings.SetPowerConfirmAttemptsOn(attempts));
}

export function SetDiscoveryAttempts(attempts: number): Promise<void> {
  return call(() => bindings.SetDiscoveryAttempts(attempts));
}

export function SetDiscoveryRetryDelayMs(delayMs: number): Promise<void> {
  return call(() => bindings.SetDiscoveryRetryDelayMs(delayMs));
}

export function SetIdentifyAttempts(attempts: number): Promise<void> {
  return call(() => bindings.SetIdentifyAttempts(attempts));
}

export function SetInitialReadTimeoutSeconds(timeoutSeconds: number): Promise<void> {
  return call(() => bindings.SetInitialReadTimeoutSeconds(timeoutSeconds));
}

export function SetOperationRetryDelayMs(delayMs: number): Promise<void> {
  return call(() => bindings.SetOperationRetryDelayMs(delayMs));
}

export function SetPresenceMissThreshold(threshold: number): Promise<void> {
  return call(() => bindings.SetPresenceMissThreshold(threshold));
}

export function SetPowerConfirmPollIntervalMs(intervalMs: number): Promise<void> {
  return call(() => bindings.SetPowerConfirmPollIntervalMs(intervalMs));
}

export function SetPowerWriteAttempts(attempts: number): Promise<void> {
  return call(() => bindings.SetPowerWriteAttempts(attempts));
}

export function SetRecoveryRetryBaseSeconds(baseSeconds: number): Promise<void> {
  return call(() => bindings.SetRecoveryRetryBaseSeconds(baseSeconds));
}

export function SetRecoveryRetryMaxSeconds(maxSeconds: number): Promise<void> {
  return call(() => bindings.SetRecoveryRetryMaxSeconds(maxSeconds));
}

export function SetScanReadPhaseTimeoutSeconds(timeoutSeconds: number): Promise<void> {
  return call(() => bindings.SetScanReadPhaseTimeoutSeconds(timeoutSeconds));
}

export function SetSleepFinalWriteTimeoutSeconds(timeoutSeconds: number): Promise<void> {
  return call(() => bindings.SetSleepFinalWriteTimeoutSeconds(timeoutSeconds));
}

export function SetSleepPrepareGapMs(gapMs: number): Promise<void> {
  return call(() => bindings.SetSleepPrepareGapMs(gapMs));
}

export function SetStationOperationTimeoutSeconds(timeoutSeconds: number): Promise<void> {
  return call(() => bindings.SetStationOperationTimeoutSeconds(timeoutSeconds));
}

export function SetStatusReadTimeoutSeconds(timeoutSeconds: number): Promise<void> {
  return call(() => bindings.SetStatusReadTimeoutSeconds(timeoutSeconds));
}

export function SetStatusRefreshTimeoutSeconds(timeoutSeconds: number): Promise<void> {
  return call(() => bindings.SetStatusRefreshTimeoutSeconds(timeoutSeconds));
}

export function SetScanDurationSeconds(durationSeconds: number): Promise<void> {
  return call(() => bindings.SetScanDurationSeconds(durationSeconds));
}

export function SetScanOnStartup(enabled: boolean): Promise<void> {
  return call(() => bindings.SetScanOnStartup(enabled));
}

export function SetStatusPollIntervalSeconds(intervalSeconds: number): Promise<void> {
  return call(() => bindings.SetStatusPollIntervalSeconds(intervalSeconds));
}

export function SetStatusPollingEnabled(enabled: boolean): Promise<void> {
  return call(() => bindings.SetStatusPollingEnabled(enabled));
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
