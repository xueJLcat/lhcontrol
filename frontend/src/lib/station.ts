import type { Capabilities, Metadata, PowerTarget, StationInfo } from './types';
import { locale, t } from './i18n.svelte';

export function powerStateValue(state: PowerTarget): number {
  return state === 'sleep' ? 0 : state === 'on' ? 1 : 2;
}

export function powerTargetLabel(state: PowerTarget): string {
  return state === 'sleep' ? t('Sleep') : state === 'on' ? t('On') : t('Standby');
}

export function isCurrentPowerState(station: StationInfo, state: PowerTarget): boolean {
  return station.powerStateConfirmed && station.powerState === powerStateValue(state);
}

export function hasCurrentChannel(station: StationInfo): boolean {
  return station.isPresent && station.scanFresh && station.channelFresh && station.channel > 0;
}

export function hasVerifiedPowerState(station: StationInfo, state: PowerTarget): boolean {
  return station.powerFresh && station.powerStateConfirmed && station.powerState === powerStateValue(state);
}

// Stable raw values per protocol: sleep 0x00, standby 0x02, on 0x09/0x0B.
const STABLE_POWER_RAW: Record<PowerTarget, readonly number[]> = {
  on: [0x09, 0x0b],
  standby: [0x02],
  sleep: [0x00]
};

// Stricter fleet-level confirmation. The backend's boot fallback also marks a
// decoded On backed by booting raw values (0x01/0x08) as confirmed, which
// would light the bulk bar's On thumb while stations are still spinning up;
// aggregate indicators therefore require the genuinely stable readback.
export function hasStableConfirmedPowerState(station: StationInfo, state: PowerTarget): boolean {
  return hasVerifiedPowerState(station, state) && STABLE_POWER_RAW[state].includes(station.rawPowerState);
}

export function isFreshBooting(station: StationInfo): boolean {
  return station.powerFresh && station.powerState === 3;
}

export function channelChangeBlockedReason(station: StationInfo): string {
  if (!station.scanFresh) {
    return t('Run a new scan before changing the channel.');
  }
  if (isFreshBooting(station)) {
    return t('Wait for the station to finish booting before changing its channel.');
  }
  return '';
}

export function maySetPower(station: StationInfo, state: PowerTarget): boolean {
  // Capability data is cached from the last GATT discovery and can become
  // incomplete after a rescan. The backend refreshes missing capabilities
  // before every power operation, so the frontend must not block that recovery
  // path. It will return a structured unsupported error if the refreshed
  // device genuinely cannot perform the target operation.
  if (isFreshBooting(station)) return false;
  return true;
}

export function canSetPower(station: StationInfo, state: PowerTarget): boolean {
  return maySetPower(station, state) &&
    !isCurrentPowerState(station, state);
}

export function stateLabel(station: StationInfo): string {
  if (locale() === 'en') return ['sleep', 'on', 'standby', 'booting'][station.powerState] || 'unknown';
  return [t('Sleep'), t('On'), t('Standby'), t('Booting')][station.powerState] || t('Unknown');
}

// Safe class-name counterpart of stateLabel: the display label prefers the
// backend-provided powerStateName, which may carry arbitrary casing or
// spacing and would break `state-...` selectors. Class names are derived
// from the numeric state so they always match the stylesheet.
export function stateClass(station: StationInfo): string {
  return ['sleep', 'on', 'standby', 'booting'][station.powerState] ?? 'unknown';
}

export function channelLabel(channel: number): string {
  return channel > 0 ? `CH ${String(channel).padStart(2, '0')}` : 'CH --';
}

function sameCapabilities(left: Capabilities | undefined, right: Capabilities | undefined): boolean {
  if (left === right) return true;
  if (!left || !right) return false;
  return left.powerRead === right.powerRead &&
    left.powerWrite === right.powerWrite &&
    left.powerNotify === right.powerNotify &&
    left.standby === right.standby &&
    left.channelRead === right.channelRead &&
    left.channelWrite === right.channelWrite &&
    left.channelNotify === right.channelNotify &&
    left.identify === right.identify &&
    left.deviceInformation === right.deviceInformation;
}

function sameMetadata(left: Metadata | undefined, right: Metadata | undefined): boolean {
  if (left === right) return true;
  if (!left || !right) return false;
  return left.manufacturer === right.manufacturer &&
    left.model === right.model &&
    left.serialNumber === right.serialNumber &&
    left.hardwareRevision === right.hardwareRevision &&
    left.firmwareRevision === right.firmwareRevision;
}

// sameStationInfo reports whether two snapshots carry identical displayable
// values. It lets list commits reuse the previous object reference for
// unchanged stations so no-op background refreshes do not re-render cards or
// retrigger CSS transitions.
export function sameStationInfo(left: StationInfo, right: StationInfo): boolean {
  if (left === right) return true;
  return left.name === right.name &&
    left.originalName === right.originalName &&
    left.address === right.address &&
    left.powerState === right.powerState &&
    left.powerStateName === right.powerStateName &&
    left.powerStateConfirmed === right.powerStateConfirmed &&
    left.rawPowerState === right.rawPowerState &&
    left.channel === right.channel &&
    left.channelConflict === right.channelConflict &&
    left.isPresent === right.isPresent &&
    left.presenceUncertain === right.presenceUncertain &&
    left.seenInLatestScan === right.seenInLatestScan &&
    left.scanFresh === right.scanFresh &&
    left.missedScans === right.missedScans &&
    left.lastSeenAt === right.lastSeenAt &&
    left.lastReadAt === right.lastReadAt &&
    left.lastPowerReadAt === right.lastPowerReadAt &&
    left.lastChannelReadAt === right.lastChannelReadAt &&
    left.metadataReadAt === right.metadataReadAt &&
    left.lastError === right.lastError &&
    left.statusFresh === right.statusFresh &&
    left.powerFresh === right.powerFresh &&
    left.channelFresh === right.channelFresh &&
    left.metadataFresh === right.metadataFresh &&
    left.connectionState === right.connectionState &&
    left.capabilitiesKnown === right.capabilitiesKnown &&
    sameCapabilities(left.capabilities, right.capabilities) &&
    sameMetadata(left.metadata, right.metadata);
}
