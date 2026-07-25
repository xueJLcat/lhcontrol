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

export function canSetPower(station: StationInfo, state: PowerTarget): boolean {
  return station.capabilitiesKnown && station.capabilities.powerWrite &&
    station.powerState !== 3 &&
    (state !== 'standby' || station.capabilities.standby) &&
    !isCurrentPowerState(station, state);
}

export function stateLabel(station: StationInfo): string {
  return station.powerStateName || ['sleep', 'on', 'standby', 'booting'][station.powerState] || 'unknown';
}

export function channelLabel(channel: number): string {
  return channel > 0 ? `CH ${String(channel).padStart(2, '0')}` : 'CH --';
}
