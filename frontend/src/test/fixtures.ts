import type { StationInfo } from '../lib/types';

const DEFAULT_OPERATION_FRESH_UNTIL = new Date(Date.now() + 45_000).toISOString();

// Shared baseline fleet fixture: a fully verified, sleeping station on channel 3.
// Individual suites override the fields they exercise.
export function createStation(overrides: Partial<StationInfo> = {}): StationInfo {
  const station = {
    name: 'LHB-TEST',
    originalName: 'LHB-TEST',
    address: '11:22:33:44:55:66',
    powerState: 0,
    powerStateName: 'sleep',
    powerStateConfirmed: true,
    rawPowerState: 0,
    channel: 3,
    channelConflict: false,
    isPresent: true,
    presenceUncertain: false,
    seenInLatestScan: true,
    scanFresh: true,
    missedScans: 0,
    lastSeenAt: '',
    lastReadAt: '',
    lastPowerReadAt: '',
    lastChannelReadAt: '',
    metadataReadAt: '',
    lastError: '',
    statusFresh: true,
    powerFresh: true,
    powerOperationallyFresh: true,
    powerOperationalFreshUntil: DEFAULT_OPERATION_FRESH_UNTIL,
    channelFresh: true,
    channelOperationallyFresh: true,
    channelOperationalFreshUntil: DEFAULT_OPERATION_FRESH_UNTIL,
    metadataFresh: false,
    connectionState: 'connected',
    capabilitiesKnown: true,
    capabilities: {
      powerRead: true,
      powerWrite: true,
      powerNotify: false,
      standby: true,
      channelRead: true,
      channelWrite: true,
      channelNotify: false,
      identify: true,
      deviceInformation: false
    },
    metadata: {
      manufacturer: '',
      model: '',
      serialNumber: '',
      hardwareRevision: '',
      firmwareRevision: ''
    },
    ...overrides
  } as StationInfo;
  // A display-stale fixture cannot be operationally fresh. Preserve the
  // explicit override so tests can model the inverse long-poll case: visible
  // but too old to authorize cached command decisions.
  if (overrides.powerFresh === false && overrides.powerOperationallyFresh === undefined) {
    station.powerOperationallyFresh = false;
  }
  if (overrides.channelFresh === false && overrides.channelOperationallyFresh === undefined) {
    station.channelOperationallyFresh = false;
  }
  return station;
}

// A station that reports itself as powered on with a verified readback, the
// default most component suites assert against.
export function createOnStation(overrides: Partial<StationInfo> = {}): StationInfo {
  return createStation({
    powerState: 1,
    powerStateName: 'on',
    rawPowerState: 0x0b,
    ...overrides
  });
}
