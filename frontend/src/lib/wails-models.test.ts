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
        rawPowerState: 1,
        channel: 3,
        capabilities: { powerRead: true, powerWrite: true },
        metadata: { model: '2.0' }
      }
    });

    expect(result.commandSent).toBe(true);
    expect(result.confirmed).toBe(false);
    expect(result.confirmationError).toBe('readback timed out');
    expect(result.station).toBeInstanceOf(station.StationInfo);
    expect(result.station.address).toBe('11:22:33:44:55:66');
    expect(result.station.capabilities.powerWrite).toBe(true);
    expect(result.station.metadata.model).toBe('2.0');
  });

  it('preserves channel confirmation data and scan terminal status', () => {
    const channel = station.ChannelChangeResult.createFrom({
      address: '11:22:33:44:55:66',
      previousChannel: 3,
      channel: 0,
      commandSent: true,
      confirmed: false,
      confirmationError: 'readback timed out',
      warnings: ['command was sent']
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
    expect(scan).toMatchObject({
      state: 'failed',
      error: 'adapter unavailable',
      warnings: ['partial cleanup'],
      found: 2
    });
  });
});
