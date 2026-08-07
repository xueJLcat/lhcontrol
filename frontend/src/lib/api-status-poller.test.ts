import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({ GetAPIStatus: vi.fn() }));

vi.mock('./backend', () => backend);

import { ApiStatusPoller } from './api-status-poller';

function makeHost() {
  return {
    isDisposed: vi.fn(() => false),
    commitStatus: vi.fn(),
    commitFailure: vi.fn(),
    reportConfigWarning: vi.fn()
  };
}

function status(overrides: Record<string, unknown> = {}) {
  return { running: true, address: '127.0.0.1:7575', error: '', warnings: [], configWritable: true, ...overrides };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('ApiStatusPoller', () => {
  it('commits a successful status to the host', async () => {
    backend.GetAPIStatus.mockResolvedValue(status());
    const host = makeHost();
    new ApiStatusPoller(host).refresh();
    await vi.waitFor(() => expect(host.commitStatus).toHaveBeenCalledWith(status()));
    expect(host.commitFailure).not.toHaveBeenCalled();
  });

  it('reports a failure as an offline API', async () => {
    backend.GetAPIStatus.mockRejectedValue(new Error('gone'));
    const host = makeHost();
    new ApiStatusPoller(host).refresh();
    await vi.waitFor(() => expect(host.commitFailure).toHaveBeenCalledWith('Error: gone'));
  });

  it('reports each new config warning once and re-reports after it recurs', async () => {
    const warning = 'config is invalid';
    backend.GetAPIStatus.mockResolvedValue(status({ warnings: [warning] }));
    const host = makeHost();
    const poller = new ApiStatusPoller(host);

    poller.refresh();
    await vi.waitFor(() => expect(host.reportConfigWarning).toHaveBeenCalledTimes(1));
    poller.refresh();
    await vi.waitFor(() => expect(host.reportConfigWarning).toHaveBeenCalledTimes(1));

    backend.GetAPIStatus.mockResolvedValueOnce(status({ warnings: [] }));
    poller.refresh();
    await vi.waitFor(() => expect(backend.GetAPIStatus).toHaveBeenCalledTimes(3));

    backend.GetAPIStatus.mockResolvedValueOnce(status({ warnings: [warning] }));
    poller.refresh();
    await vi.waitFor(() => expect(host.reportConfigWarning).toHaveBeenCalledTimes(2));
    expect(host.reportConfigWarning).toHaveBeenNthCalledWith(2, warning);
  });

  it('never lets an out-of-order response overwrite a newer poll', async () => {
    let resolveOld!: (value: unknown) => void;
    backend.GetAPIStatus
      .mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve; }))
      .mockResolvedValueOnce(status({ address: 'new' }));
    const host = makeHost();
    const poller = new ApiStatusPoller(host);

    poller.refresh();
    poller.refresh();
    await vi.waitFor(() => expect(host.commitStatus).toHaveBeenCalledWith(status({ address: 'new' })));

    resolveOld(status({ address: 'old' }));
    await vi.waitFor(() => expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2));
    expect(host.commitStatus).toHaveBeenCalledTimes(1);
    expect(host.commitStatus).toHaveBeenCalledWith(status({ address: 'new' }));
  });

  it('ignores responses after disposal', async () => {
    let resolveStatus!: (value: unknown) => void;
    backend.GetAPIStatus.mockReturnValueOnce(new Promise((resolve) => { resolveStatus = resolve; }));
    const host = makeHost();
    const poller = new ApiStatusPoller(host);

    poller.refresh();
    host.isDisposed.mockReturnValue(true);
    resolveStatus(status());
    await vi.waitFor(() => expect(backend.GetAPIStatus).toHaveBeenCalled());
    await Promise.resolve();
    expect(host.commitStatus).not.toHaveBeenCalled();
  });

  it('stops polling after dispose', async () => {
    vi.useFakeTimers();
    backend.GetAPIStatus.mockResolvedValue(status());
    const poller = new ApiStatusPoller(makeHost());
    poller.start(15_000);
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(1);

    poller.dispose();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(1);
  });

  it('replaces the existing timer when the interval changes', async () => {
    vi.useFakeTimers();
    backend.GetAPIStatus.mockResolvedValue(status());
    const poller = new ApiStatusPoller(makeHost());
    poller.start(10_000);
    poller.start(20_000);
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(10_000);
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(3);
    poller.dispose();
  });
});
