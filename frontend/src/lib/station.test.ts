import { describe, expect, it } from 'vitest';
import { sameStationInfo, stateClass } from './station';
import type { StationInfo } from './types';

function station(overrides: Partial<StationInfo> = {}): StationInfo {
  return {
    name: 'LHB-A',
    originalName: 'LHB-A',
    address: '11:22:33:44:55:66',
    powerState: 1,
    powerStateName: 'on',
    powerStateConfirmed: true,
    rawPowerState: 0x0b,
    channel: 3,
    channelConflict: false,
    isPresent: true,
    presenceUncertain: false,
    seenInLatestScan: true,
    scanFresh: true,
    missedScans: 0,
    lastSeenAt: '2026-01-01T00:00:00Z',
    lastReadAt: '',
    lastPowerReadAt: '',
    lastChannelReadAt: '',
    metadataReadAt: '',
    lastError: '',
    statusFresh: true,
    powerFresh: true,
    channelFresh: true,
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
      manufacturer: 'Valve',
      model: '',
      serialNumber: '',
      hardwareRevision: '',
      firmwareRevision: ''
    },
    ...overrides
  } as StationInfo;
}

describe('stateClass', () => {
  it('maps numeric states to stylesheet-safe class names', () => {
    expect(stateClass(station({ powerState: 0 }))).toBe('sleep');
    expect(stateClass(station({ powerState: 1 }))).toBe('on');
    expect(stateClass(station({ powerState: 2 }))).toBe('standby');
    expect(stateClass(station({ powerState: 3 }))).toBe('booting');
  });

  it('falls back to unknown for unreported or out-of-range states', () => {
    expect(stateClass(station({ powerState: -1 }))).toBe('unknown');
    expect(stateClass(station({ powerState: 7 }))).toBe('unknown');
  });

  it('ignores the backend label so odd casing cannot break selectors', () => {
    expect(stateClass(station({ powerState: 1, powerStateName: 'ON now' }))).toBe('on');
  });
});

describe('sameStationInfo', () => {
  it('accepts the same reference and equal snapshots', () => {
    const value = station();
    expect(sameStationInfo(value, value)).toBe(true);
    expect(sameStationInfo(value, station())).toBe(true);
  });

  it('rejects differing scalar fields', () => {
    const base = station();
    for (const overrides of [
      { name: 'LHB-B' },
      { powerState: 0 },
      { powerStateConfirmed: false },
      { channel: 0 },
      { channelConflict: true },
      { isPresent: false },
      { presenceUncertain: true },
      { missedScans: 1 },
      { lastPowerReadAt: '2026-01-02T00:00:00Z' },
      { lastError: 'read failed' },
      { powerFresh: false },
      { connectionState: 'disconnected' },
      { capabilitiesKnown: false }
    ] as Partial<StationInfo>[]) {
      expect(sameStationInfo(base, station(overrides))).toBe(false);
    }
  });

  it('rejects differing capabilities and metadata', () => {
    const base = station();
    expect(sameStationInfo(base, station({
      capabilities: { ...base.capabilities, channelRead: false }
    }))).toBe(false);
    expect(sameStationInfo(base, station({
      metadata: { ...base.metadata, firmwareRevision: '1.2.3' }
    }))).toBe(false);
  });
});
