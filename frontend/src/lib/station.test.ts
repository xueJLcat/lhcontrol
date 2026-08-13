import { describe, expect, it } from 'vitest';
import {
  canSetPower,
  hasCurrentChannel,
  hasOperationallyCurrentChannel,
  hasStableConfirmedPowerState,
  sameStationInfo,
  stateClass
} from './station';
import type { StationInfo } from './types';
import { createOnStation } from '../test/fixtures';

function station(overrides: Partial<StationInfo> = {}): StationInfo {
  return createOnStation({
    name: 'LHB-A',
    originalName: 'LHB-A',
    lastSeenAt: '2026-01-01T00:00:00Z',
    metadata: {
      manufacturer: 'Valve',
      model: '',
      serialNumber: '',
      hardwareRevision: '',
      firmwareRevision: ''
    },
    ...overrides
  });
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

describe('channel freshness', () => {
  it('keeps display and operation freshness as separate decisions', () => {
    const displayOnly = station({ channelFresh: true, channelOperationallyFresh: false });
    expect(hasCurrentChannel(displayOnly)).toBe(true);
    expect(hasOperationallyCurrentChannel(displayOnly)).toBe(false);
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
      { powerOperationallyFresh: false },
      { channelOperationallyFresh: false },
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

describe('hasStableConfirmedPowerState', () => {
  it('accepts a confirmed on backed by a stable raw readback', () => {
    expect(hasStableConfirmedPowerState(station({ rawPowerState: 0x09 }), 'on')).toBe(true);
    expect(hasStableConfirmedPowerState(station({ rawPowerState: 0x0b }), 'on')).toBe(true);
  });

  it('rejects the boot fallback: decoded on with booting raw values', () => {
    expect(hasStableConfirmedPowerState(station({ rawPowerState: 0x01 }), 'on')).toBe(false);
    expect(hasStableConfirmedPowerState(station({ rawPowerState: 0x08 }), 'on')).toBe(false);
  });

  it('rejects unconfirmed or stale stations', () => {
    expect(hasStableConfirmedPowerState(station({ powerStateConfirmed: false }), 'on')).toBe(false);
    expect(hasStableConfirmedPowerState(station({ powerFresh: false }), 'on')).toBe(false);
    expect(hasStableConfirmedPowerState(station({ powerState: 0 }), 'on')).toBe(false);
  });

  it('keeps the exact-raw semantics for standby and sleep', () => {
    expect(hasStableConfirmedPowerState(station({ powerState: 2, powerStateName: 'standby', rawPowerState: 0x02 }), 'standby')).toBe(true);
    expect(hasStableConfirmedPowerState(station({ powerState: 2, powerStateName: 'standby', rawPowerState: 0x01 }), 'standby')).toBe(false);
    expect(hasStableConfirmedPowerState(station({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x00 }), 'sleep')).toBe(true);
    expect(hasStableConfirmedPowerState(station({ powerState: 0, powerStateName: 'sleep', rawPowerState: 0x08 }), 'sleep')).toBe(false);
  });
});

describe('canSetPower', () => {
  it('blocks a fresh confirmed target but allows its stale cache to be revalidated', () => {
    expect(canSetPower(station({ powerFresh: true }), 'on')).toBe(false);
    expect(canSetPower(station({ powerFresh: false }), 'on')).toBe(true);
  });

  it('revalidates display-fresh state after the fixed operation window expires', () => {
    expect(canSetPower(station({
      powerFresh: true,
      powerOperationallyFresh: false
    }), 'on')).toBe(true);
    expect(canSetPower(station({
      powerState: 3,
      powerStateName: 'booting',
      rawPowerState: 0x01,
      powerFresh: true,
      powerOperationallyFresh: false
    }), 'on')).toBe(true);
  });
});
