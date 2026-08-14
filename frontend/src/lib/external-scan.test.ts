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
    canAdoptUnknownScan: () => true,
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

  it('keeps a failed terminal recoverable when its status commit is rejected mid-read', async () => {
    const { host } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 5 });
    // Another status owner claims the epoch while the failure reads are in
    // flight, so the terminal status commit must be rejected.
    (host.getCurrentStationInfo as ReturnType<typeof vi.fn>).mockImplementation(async () => {
      host.beginStatusOperation();
      return [];
    });
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      state: 'failed', found: 0, error: 'boom', warnings: []
    });

    await coordinator.handleFailed({ id: 5, error: 'boom' });

    // The commit was rejected before clearing the recovery epochs, so the
    // periodic check can still retry instead of dropping the failure forever.
    expect(coordinator.hasPendingRecovery()).toBe(true);
    expect(host.notifyExternalScanFailure).not.toHaveBeenCalled();
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

  it('does not adopt a scan that ended before the adoption check', async () => {
    const { host, state } = createHost();
    mockIsScanning(host, false);
    const coordinator = new ExternalScanCoordinator(host);

    await coordinator.adoptUnknown();

    expect(host.isScanning).toHaveBeenCalledOnce();
    expect(state.externalScanning).toBe(false);
    expect(host.beginScanTimer).not.toHaveBeenCalled();
  });

  it('does not adopt an internal scan whose owner starts during the adoption check', async () => {
    let resolveScanning!: (value: boolean) => void;
    let canAdopt = true;
    const { host, state } = createHost({
      canAdoptUnknownScan: () => canAdopt,
      isScanning: vi.fn(() => new Promise<boolean>((resolve) => { resolveScanning = resolve; }))
    });
    const coordinator = new ExternalScanCoordinator(host);

    const adoption = coordinator.adoptUnknown();
    canAdopt = false;
    resolveScanning(true);
    await adoption;

    expect(state.externalScanning).toBe(false);
    expect(host.beginScanTimer).not.toHaveBeenCalled();
  });

  it('keeps a tracked terminal recovery pending when the station list read rejects', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.markRecoveryPending();
    expect(coordinator.hasPendingRecovery()).toBe(true);
    (host.getCurrentStationInfo as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('backend busy'));

    // A transient rejection must resolve without throwing (so the periodic
    // check does not surface a misleading error) and must leave the recovery
    // epochs pending for a retry on the next tick.
    await expect(coordinator.recoverTrackedTerminal(1, state.statusEpoch, new Map())).resolves.toBeUndefined();

    expect(host.applyStationList).not.toHaveBeenCalled();
    expect(coordinator.hasPendingRecovery()).toBe(true);
  });

  it('keeps a tracked terminal recovery pending when its status commit is rejected', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.markRecoveryPending();
    expect(coordinator.hasPendingRecovery()).toBe(true);
    // Another status owner claims the epoch while the terminal reads are in
    // flight, so the terminal status commit must be rejected.
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockImplementation(async () => {
      host.beginStatusOperation();
      return { state: 'cancelled', found: 1, warnings: [] };
    });

    await coordinator.recoverTrackedTerminal(1, state.statusEpoch, new Map());

    // The commit was rejected before clearing the recovery epochs, so the
    // periodic check can still retry instead of dropping the outcome forever.
    expect(coordinator.hasPendingRecovery()).toBe(true);
    expect(state.statusMessages).toEqual([]);
  });

  it('clears the recovery epochs once the tracked terminal status commits', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.markRecoveryPending();
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      state: 'cancelled', found: 1, warnings: []
    });

    await coordinator.recoverTrackedTerminal(1, state.statusEpoch, new Map());

    expect(coordinator.hasPendingRecovery()).toBe(false);
    expect(state.statusMessages[state.statusMessages.length - 1]).toContain('External scan stopped');
  });

  it('keeps an untracked recovery pending when its status commit is rejected', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    await coordinator.handleFailed({ id: 9, error: 'boom' });
    expect(coordinator.hasPendingTerminal()).toBe(true);
    // Another status owner claims the epoch while the recovery reads are in
    // flight, so the terminal status commit must be rejected.
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockImplementation(async () => {
      host.beginStatusOperation();
      return { state: 'failed', found: 0, error: 'boom', warnings: [] };
    });

    await coordinator.adoptUnknown();

    // The rejected commit must not clear the recovery epochs, so the periodic
    // check can retry instead of dropping the terminal outcome forever.
    expect(coordinator.hasPendingTerminal()).toBe(false);
    expect(coordinator.hasPendingRecovery()).toBe(true);
    expect(state.statusMessages.filter((message) => message.includes('External scan failed'))).toEqual([]);
    expect(host.notifyExternalScanFailure).not.toHaveBeenCalled();
  });

  it('clears the recovery epochs once the untracked terminal status commits', async () => {
    const { host } = createHost();
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      state: 'failed', found: 0, error: 'boom', warnings: []
    });
    const coordinator = new ExternalScanCoordinator(host);
    await coordinator.handleFailed({ id: 9, error: 'boom' });

    await coordinator.adoptUnknown();

    expect(coordinator.hasPendingRecovery()).toBe(false);
  });

  it('drops a pending terminal recovery once a newer status owner supersedes it', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.markRecoveryPending();
    expect(coordinator.hasPendingRecovery()).toBe(true);
    // A newer owner claims the status line before the recovery reads run.
    // Status epochs only advance, so the terminal message can never commit
    // again; the recovery must be dropped instead of re-running every poll.
    state.statusEpoch += 2;

    await coordinator.recoverTrackedTerminal(1, state.statusEpoch, new Map());

    expect(coordinator.hasPendingRecovery()).toBe(false);
    expect(state.statusMessages).toEqual([]);
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

  it('consumes the stopped adopted scan\'s delayed terminal instead of remembering it', async () => {
    const { host, state } = createHost();
    mockIsScanning(host, true);
    const coordinator = new ExternalScanCoordinator(host);
    // Adopted scans carry no id: only the polling observation knows them.
    await coordinator.adoptUnknown();
    expect(state.externalScanning).toBe(true);
    mockIsScanning(host, false);

    const outcome = await coordinator.finishStop(state.scanEpoch, () => true);
    expect(outcome).toBe('recovered');
    expect(coordinator.hasPendingTerminal()).toBe(false);

    // The backend still emits the terminal event for the stopped scan. It
    // must be consumed, not remembered: a remembered terminal would make the
    // next poll replay a full recovery over already-applied state.
    await coordinator.handleCancelled({ id: 8 });
    expect(coordinator.hasPendingTerminal()).toBe(false);

    // Consuming still advances the id gate, so a delayed start for the same
    // scan cannot resurrect the dead scan.
    coordinator.handleStarted({ id: 8 });
    expect(state.externalScanning).toBe(false);
  });

  it('keeps recovery pending when a completed event reads a non-terminal scan status', async () => {
    const { host, state } = createHost();
    const coordinator = new ExternalScanCoordinator(host);
    coordinator.handleStarted({ id: 3 });
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      state: 'running', found: 0, warnings: []
    });

    await coordinator.handleCompleted({ id: 3, stations: [] });

    // The running status belongs to a scan that started after this one
    // finished; the terminal recovery must stay pending for a retry instead
    // of clearing its epochs on the wrong scan's status.
    expect(coordinator.hasPendingRecovery()).toBe(true);
    expect(state.externalScanning).toBe(false);
  });

  it('retries untracked recovery while the scan status is non-terminal', async () => {
    const { host, state } = createHost();
    (host.getScanStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
      state: 'starting', found: 0, warnings: []
    });
    const coordinator = new ExternalScanCoordinator(host);
    await coordinator.handleFailed({ id: 9, error: 'boom' });
    expect(coordinator.hasPendingTerminal()).toBe(true);

    await coordinator.adoptUnknown();

    expect(coordinator.hasPendingTerminal()).toBe(false);
    expect(coordinator.hasPendingRecovery()).toBe(true);
    expect(state.statusMessages.filter((message) => message.includes('External scan failed'))).toEqual([]);
  });

  it('keeps remembering untracked terminals for scans begun after a recovered stop', async () => {
    const { host, state } = createHost();
    mockIsScanning(host, true);
    const coordinator = new ExternalScanCoordinator(host);
    await coordinator.adoptUnknown();
    mockIsScanning(host, false);
    await coordinator.finishStop(state.scanEpoch, () => true);

    // A new external scan starts after the stop recovered; its own lifecycle
    // must not be shadowed by the earlier stop's pending terminal consumption.
    coordinator.handleStarted({ id: 9 });
    expect(state.externalScanning).toBe(true);
    await coordinator.handleCompleted({ id: 9, stations: [] });
    expect(state.externalScanning).toBe(false);

    // A genuinely untracked terminal afterwards is remembered as before.
    await coordinator.handleFailed({ id: 11, error: 'radio failure' });
    expect(coordinator.hasPendingTerminal()).toBe(true);
  });
});
