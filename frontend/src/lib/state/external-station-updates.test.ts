import { describe, expect, it, vi } from 'vitest';

import type { StationInfo } from '../types';
import { ExternalStationUpdateCoordinator } from './external-station-updates';

function station(address: string, name = address): StationInfo {
  return {
    name,
    originalName: name,
    address,
    powerState: 1,
    powerStateName: 'on',
    rawPowerState: 11,
    channel: 1,
    isPresent: true,
    presenceUncertain: false,
    seenInLatestScan: true,
    scanFresh: true,
    missedScans: 0,
    statusFresh: true,
    powerFresh: true,
    powerOperationallyFresh: true,
    channelFresh: true,
    channelOperationallyFresh: true,
    metadataFresh: true,
    powerStateConfirmed: true,
    channelConflict: false,
    lastSeenAt: '',
    lastReadAt: '',
    lastPowerReadAt: '',
    lastChannelReadAt: '',
    metadataReadAt: '',
    lastError: '',
    connectionState: 'disconnected',
    capabilitiesKnown: true,
    capabilities: {
      powerRead: true,
      powerWrite: true,
      powerNotify: false,
      standby: true,
      channelRead: true,
      channelWrite: true,
      channelNotify: false,
      identify: false,
      deviceInformation: false
    },
    metadata: {}
  } as unknown as StationInfo;
}

function coordinator(options: {
  isDisposed?: () => boolean;
  ownsSnapshot?: () => boolean;
  isStationBusy?: (address: string) => boolean;
} = {}) {
  const dependencies = {
    isDisposed: vi.fn(options.isDisposed ?? (() => false)),
    localListOperationOwnsSnapshot: vi.fn(options.ownsSnapshot ?? (() => false)),
    isStationBusy: vi.fn(options.isStationBusy ?? (() => false)),
    invalidatePendingLists: vi.fn(),
    mergeStations: vi.fn((_: StationInfo[]) => {})
  };
  return { coordinator: new ExternalStationUpdateCoordinator(dependencies), dependencies };
}

describe('ExternalStationUpdateCoordinator', () => {
  it('merges a newer snapshot and invalidates pending lists', () => {
    const { coordinator: updateCoordinator, dependencies } = coordinator();
    updateCoordinator.handle({ id: 3, source: 'http-power', stations: [station('AA')] });
    expect(dependencies.invalidatePendingLists).toHaveBeenCalledOnce();
    expect(dependencies.mergeStations).toHaveBeenCalledWith([expect.objectContaining({ address: 'AA' })]);
  });

  it('drops snapshots with an id at or below the last applied id', () => {
    const { coordinator: updateCoordinator, dependencies } = coordinator();
    updateCoordinator.handle({ id: 5, source: 'http-power', stations: [station('AA')] });
    dependencies.mergeStations.mockClear();
    updateCoordinator.handle({ id: 5, source: 'http-power', stations: [station('BB')] });
    updateCoordinator.handle({ id: 4, source: 'http-power', stations: [station('CC')] });
    expect(dependencies.mergeStations).not.toHaveBeenCalled();
  });

  it('applies unsequenced snapshots but never advances the id gate', () => {
    const { coordinator: updateCoordinator, dependencies } = coordinator();
    updateCoordinator.handle({ id: 0, source: 'legacy', stations: [station('AA')] });
    expect(dependencies.mergeStations).toHaveBeenCalledOnce();
    // A later sequenced snapshot with a low id must still apply because the
    // unsequenced one did not move the gate.
    dependencies.mergeStations.mockClear();
    updateCoordinator.handle({ id: 1, source: 'http-power', stations: [station('BB')] });
    expect(dependencies.mergeStations).toHaveBeenCalledOnce();
  });

  it('advances the id gate but skips the merge while a local list operation owns the snapshot', () => {
    const ownsSpy = vi.fn(() => true);
    const { coordinator: updateCoordinator, dependencies } = coordinator({ ownsSnapshot: ownsSpy });
    updateCoordinator.handle({ id: 7, source: 'http-power', stations: [station('AA')] });
    expect(dependencies.mergeStations).not.toHaveBeenCalled();
    // The advanced gate must still drop an older snapshot once the local
    // operation released ownership.
    ownsSpy.mockReturnValue(false);
    updateCoordinator.handle({ id: 6, source: 'http-power', stations: [station('BB')] });
    expect(dependencies.mergeStations).not.toHaveBeenCalled();
  });

  it('filters busy stations and empty addresses from merged snapshots', () => {
    const { coordinator: updateCoordinator, dependencies } = coordinator({
      isStationBusy: (address) => address === 'BUSY'
    });
    updateCoordinator.handle({
      id: 2,
      source: 'http-power',
      stations: [station('BUSY'), station('FREE'), { address: '' } as unknown as StationInfo]
    });
    expect(dependencies.mergeStations).toHaveBeenCalledWith([expect.objectContaining({ address: 'FREE' })]);
  });

  it('ignores events when disposed or malformed', () => {
    const { coordinator: updateCoordinator, dependencies } = coordinator({ isDisposed: () => true });
    updateCoordinator.handle({ id: 9, source: 'http-power', stations: [station('AA')] });
    expect(dependencies.mergeStations).not.toHaveBeenCalled();
    dependencies.isDisposed.mockReturnValue(false);
    updateCoordinator.handle({ id: 9, source: 'http-power' } as unknown as Parameters<ExternalStationUpdateCoordinator['handle']>[0]);
    expect(dependencies.mergeStations).not.toHaveBeenCalled();
  });
});
