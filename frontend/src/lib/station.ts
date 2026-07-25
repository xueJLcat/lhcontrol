import type { PowerTarget, StationInfo } from './types';

export function powerStateValue(state: PowerTarget): number {
  return state === 'sleep' ? 0 : state === 'on' ? 1 : 2;
}

export function powerTargetLabel(state: PowerTarget): string {
  return state === 'sleep' ? 'Sleep' : state === 'on' ? 'On' : 'Standby';
}

export function isCurrentPowerState(station: StationInfo, state: PowerTarget): boolean {
  return station.powerStateConfirmed && station.powerState === powerStateValue(state);
}

export function maySetPower(station: StationInfo, state: PowerTarget): boolean {
  // Capability data is cached from the last GATT discovery and can become
  // incomplete after a rescan. The backend refreshes missing capabilities
  // before every power operation, so the frontend must not block that recovery
  // path. It will return a structured unsupported error if the refreshed
  // device genuinely cannot perform the target operation.
  return station.powerState !== 3;
}

export function canSetPower(station: StationInfo, state: PowerTarget): boolean {
  return maySetPower(station, state) &&
    !isCurrentPowerState(station, state);
}

export function stateLabel(station: StationInfo): string {
  return station.powerStateName || ['sleep', 'on', 'standby', 'booting'][station.powerState] || 'unknown';
}

export function channelLabel(channel: number): string {
  return channel > 0 ? `CH ${String(channel).padStart(2, '0')}` : 'CH --';
}
