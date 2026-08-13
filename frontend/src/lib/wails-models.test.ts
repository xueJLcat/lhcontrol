import { describe, expect, it } from 'vitest';
import { main, station } from '../../wailsjs/go/models';

describe('generated Wails result models', () => {
  it('preserves configuration health fields', () => {
    const status = main.APIStatus.createFrom({
      running: true,
      address: '127.0.0.1:7575',
      error: '',
      warnings: ['invalid configuration was preserved'],
      configWritable: true
    });

    expect(status.warnings).toEqual(['invalid configuration was preserved']);
    expect(status.configWritable).toBe(true);
  });

  it('preserves an unconfirmed power result and its nested station', () => {
    const result = station.PowerActionResult.createFrom({
      commandSent: true,
      confirmed: false,
      confirmationError: 'readback timed out',
      station: {
        name: 'LHB-TEST',
        originalName: 'LHB-TEST',
        address: '11:22:33:44:55:66',
        powerState: 1,
        powerStateName: 'on',
        powerStateConfirmed: false,
        powerOperationallyFresh: false,
        powerOperationalFreshUntil: '2026-08-13T12:00:45Z',
        rawPowerState: 1,
        channel: 3,
        channelOperationallyFresh: false,
        channelOperationalFreshUntil: '2026-08-13T12:00:45Z',
        capabilities: { powerRead: true, powerWrite: true },
        metadata: { model: '2.0' }
      }
    });

    expect(result.commandSent).toBe(true);
    expect(result.confirmed).toBe(false);
    expect(result.confirmationError).toBe('readback timed out');
    expect(result.station).toBeInstanceOf(station.StationInfo);
    expect(result.station.address).toBe('11:22:33:44:55:66');
    expect(result.station.powerOperationallyFresh).toBe(false);
    expect(result.station.powerOperationalFreshUntil).toBe('2026-08-13T12:00:45Z');
    expect(result.station.channelOperationallyFresh).toBe(false);
    expect(result.station.channelOperationalFreshUntil).toBe('2026-08-13T12:00:45Z');
    expect(result.station.capabilities.powerWrite).toBe(true);
    expect(result.station.metadata.model).toBe('2.0');
  });

  it('preserves the cached no-op power result', () => {
    const result = station.PowerActionResult.createFrom({
      commandSent: false,
      skipped: true,
      reason: 'already at target state',
      confirmed: true,
      station: {
        name: 'LHB-TEST',
        originalName: 'LHB-TEST',
        address: '11:22:33:44:55:66',
        powerState: 1,
        powerStateName: 'on',
        powerStateConfirmed: true,
        rawPowerState: 0x0b,
        channel: 3,
        capabilities: { powerRead: true, powerWrite: true },
        metadata: {}
      }
    });

    expect(result.commandSent).toBe(false);
    expect(result.skipped).toBe(true);
    expect(result.reason).toBe('already at target state');
    expect(result.confirmed).toBe(true);
    expect(result.station).toBeInstanceOf(station.StationInfo);
  });

  it('preserves channel confirmation data and scan terminal status', () => {
    const channel = station.ChannelChangeResult.createFrom({
      address: '11:22:33:44:55:66',
      previousChannel: 3,
      channel: 0,
      commandSent: true,
      confirmed: false,
      confirmationError: 'readback timed out',
      warnings: ['command was sent'],
      station: {
        name: 'LHB-TEST',
        originalName: 'LHB-TEST',
        address: '11:22:33:44:55:66',
        powerState: 1,
        powerStateName: 'on',
        powerStateConfirmed: true,
        rawPowerState: 1,
        channel: 3,
        capabilities: { channelRead: true, channelWrite: true },
        metadata: {}
      }
    });
    const scan = station.ScanStatus.createFrom({
      state: 'failed',
      startedAt: '2026-07-27T12:00:00Z',
      completedAt: '2026-07-27T12:00:05Z',
      error: 'adapter unavailable',
      warnings: ['partial cleanup'],
      found: 2
    });

    expect(channel).toMatchObject({
      commandSent: true,
      confirmed: false,
      confirmationError: 'readback timed out',
      warnings: ['command was sent']
    });
    expect(channel.station).toBeInstanceOf(station.StationInfo);
    expect(channel.station.channel).toBe(3);
    expect(scan).toMatchObject({
      state: 'failed',
      error: 'adapter unavailable',
      warnings: ['partial cleanup'],
      found: 2
    });
  });
});
