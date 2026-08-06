import { describe, expect, it, vi } from 'vitest';
import { ExternalScanCoordinator, type ExternalScanHost } from './external-scan';

interface HostState {
  disposed: boolean;
  externalScanning: boolean;
  stoppingScan: boolean;
  scanEpoch: number;
  statusEpoch: number;
  statusMessages: string[];
}

function createHost(overrides: Partial<ExternalScanHost> = {}): { host: ExternalScanHost; state: HostState } {
  const state: HostState = {
    disposed: false,
    externalScanning: false,
    stoppingScan: false,
    scanEpoch: 1,
    statusEpoch: 1,
    statusMessages: []
  };
  const host: ExternalScanHost = {
    isDisposed: () => state.disposed,
    localScanRunning: () => false,
    externalScanning: () => state.externalScanning,
    setExternalScanning: (value) => state.externalScanning = value,
    scanEpoch: () => state.scanEpoch,
    statusEpoch: () => state.statusEpoch,
    beginScanEpoch: () => ++state.scanEpoch,
    beginStatusOperation: () => ++state.statusEpoch,
    canCommitOperation: (epoch) => !state.disposed && epoch === state.scanEpoch,
    canCommitStatus: (epoch) => !state.disposed && epoch === state.statusEpoch,
    nextListRevision: () => 1,
    isListRevisionCurrent: () => true,
    snapshotStationRevisions: () => new Map<string, number>(),
    prepareForScan: vi.fn(),
    applyStationList: vi.fn(() => true),
    seenInLatestScanCount: () => 2,
    knownStationCount: () => 3,
    setStatusMessage: (message) => state.statusMessages.push(message),
    setStoppingScan: (value) => state.stoppingScan = value,
    beginScanTimer: vi.fn(),
    maybeEndScanTimer: vi.fn(),
    isScanning: vi.fn().mockResolvedValue(false),
    getScanStatus: vi.fn().mockResolvedValue({ state: 'completed', found: 2, warnings: [] }),
    getCurrentStationInfo: vi.fn().mockResolvedValue([]),
    notifyExternalScanFailure: vi.fn(),
    ...overrides
  };
  return { host, state };
}

function mockIsScanning(host: ExternalScanHost, value: boolean) {
  (host.isScanning as ReturnType<typeof vi.fn>).mockResolvedValue(value);
}

describe('ExternalScanCoordinator', () => {
  it('begins a tracked external scan and clears stopping state', () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    state.stoppingScan = true;

    coordinator.handleStarted({ id: 3 });

    expect(state.externalScanning).toBe(true);
    expect(state.stoppingScan).toBe(false);
    expect(state.statusMessages).toEqual(['Preparing external scan...']);
  });

  it('drops a start event whose id is not newer than the last terminal', () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);

    coordinator.handleStarted({ id: 5 });
    coordinator.handleStarted({ id: 5 });

    // Only the first begin should have produced the preparing message.
    expect(state.statusMessages).toEqual(['Preparing external scan...']);
  });

  it('claims a terminal event for the tracked scan and reports completion', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 7 });

    await coordinator.handleCompleted({ id: 7, stations: [] });

    expect(state.externalScanning).toBe(false);
    expect(state.statusMessages[state.statusMessages.length - 1]).toContain('External scan completed');
  });

  it('ignores a terminal event for a different tracked scan id', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 7 });

    await coordinator.handleCompleted({ id: 8, stations: [] });

    expect(state.externalScanning).toBe(true);
  });

  it('remembers an untracked terminal and recovers it on adoption', async () => {
    const { host, state } = createHost();
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      state: 'failed', found: 0, error: 'boom', warnings: []
    });
    const coordinator = new ExternalScanCoordinator(host);

    expect(coordinator.hasPendingTerminal()).toBe(false);
    await coordinator.handleFailed({ id: 9, error: 'boom' });

    // Untracked failures are remembered, not reported directly; the recovery
    // read owns the authoritative outcome.
    expect(coordinator.hasPendingTerminal()).toBe(true);
    expect(host.notifyExternalScanFailure).not.toHaveBeenCalled();

    await coordinator.adoptUnknown();
    expect(coordinator.hasPendingTerminal()).toBe(false);
    expect(state.statusMessages[state.statusMessages.length - 1]).toContain('External scan failed: boom');
  });

  it('reports a claimed external failure as a toast', async () => {
    const { host } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 2 });

    await coordinator.handleFailed({ id: 2, error: 'adapter lost' });

    expect(host.notifyExternalScanFailure).toHaveBeenCalledWith('External scan failed: adapter lost');
  });

  it('keeps adopting while the backend scan is still running', async () => {
    const { host, state } = createHost();
    mockIsScanning(host, true);
    const coordinator = new ExternalScanCoordinator(host);
    // Adopted scans have no id; simulate the adoption.
    coordinator.handleStarted({ id: 1 });
    coordinator.resetForLocalScan();

    await coordinator.handleCompleted({ id: 4, stations: [] });

    // Still running: no claim, and nothing is remembered while the scan is
    // being tracked as active.
    expect(state.externalScanning).toBe(true);
    expect(coordinator.hasPendingTerminal()).toBe(false);
  });

  it('claims an unknown terminal once the backend scan has ended', async () => {
    const { host, state } = createHost();
    mockIsScanning(host, false);
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 1 });
    coordinator.resetForLocalScan();

    await coordinator.handleCompleted({ id: 4, stations: [] });

    expect(state.externalScanning).toBe(false);
    expect(coordinator.hasPendingTerminal()).toBe(false);
    expect(state.statusMessages[state.statusMessages.length - 1]).toContain('External scan completed');
  });

  it('resets tracked state for a superseding local scan', () => {
    const { host } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 2 });

    coordinator.resetForLocalScan();

    expect(coordinator.hasPendingTerminal()).toBe(false);
    expect(coordinator.hasPendingRecovery()).toBe(false);
  });

  it('marks recovery epochs for the current scan and status operations', () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);

    expect(coordinator.hasPendingRecovery()).toBe(false);
    coordinator.markRecoveryPending();
    expect(coordinator.hasPendingRecovery()).toBe(true);

    // A newer scan epoch invalidates the pending recovery.
    state.scanEpoch += 1;
    expect(coordinator.hasPendingRecovery()).toBe(false);
  });

  it('finishes a stop whose scan already ended and writes the terminal status', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 6 });
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      state: 'cancelled', found: 1, warnings: []
    });

    const outcome = await coordinator.finishStop(state.scanEpoch, () => true);

    expect(outcome).toBe('recovered');
    expect(state.externalScanning).toBe(false);
    expect(state.stoppingScan).toBe(false);
    expect(state.statusMessages[state.statusMessages.length - 1]).toContain('External scan stopped');
  });

  it('aborts finishing a stop whose request generation was superseded', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 6 });

    const outcome = await coordinator.finishStop(state.scanEpoch, () => false);

    expect(outcome).toBe('aborted');
    expect(state.externalScanning).toBe(true);
  });
});
