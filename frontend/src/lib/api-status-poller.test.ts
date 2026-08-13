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

  it('serializes concurrent refreshes and coalesces them into one trailing poll', async () => {
    let resolveFirst!: (value: unknown) => void;
    let resolveSecond!: (value: unknown) => void;
    backend.GetAPIStatus
      .mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve; }))
      .mockReturnValueOnce(new Promise((resolve) => { resolveSecond = resolve; }));
    const host = makeHost();
    const poller = new ApiStatusPoller(host);

    const first = poller.refresh();
    const second = poller.refresh();
    const third = poller.refresh();
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(1);

    resolveFirst(status({ address: 'first' }));
    await vi.waitFor(() => expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2));
    expect(host.commitStatus).toHaveBeenCalledWith(status({ address: 'first' }));

    resolveSecond(status({ address: 'second' }));
    await Promise.all([first, second, third]);
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2);
    expect(host.commitStatus).toHaveBeenNthCalledWith(2, status({ address: 'second' }));
  });

  it('lets an explicit restart supersede a hung request', async () => {
    let resolveOld!: (value: unknown) => void;
    backend.GetAPIStatus
      .mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve; }))
      .mockResolvedValueOnce(status({ address: 'new' }));
    const host = makeHost();
    const poller = new ApiStatusPoller(host);

    const oldRequest = poller.start(10_000);
    await poller.start(20_000);
    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2);
    expect(host.commitStatus).toHaveBeenCalledOnce();
    expect(host.commitStatus).toHaveBeenCalledWith(status({ address: 'new' }));

    resolveOld(status({ address: 'old' }));
    await oldRequest;
    expect(host.commitStatus).toHaveBeenCalledOnce();
    poller.dispose();
  });

  it('settles active and trailing callers through a superseding restart', async () => {
    backend.GetAPIStatus
      .mockReturnValueOnce(new Promise(() => {}))
      .mockResolvedValueOnce(status({ address: 'replacement' }));
    const host = makeHost();
    const poller = new ApiStatusPoller(host);

    let activeSettled = false;
    let trailingSettled = false;
    void poller.refresh().then(() => { activeSettled = true; });
    void poller.refresh().then(() => { trailingSettled = true; });

    await poller.start(20_000);
    await Promise.resolve();

    expect(backend.GetAPIStatus).toHaveBeenCalledTimes(2);
    expect(host.commitStatus).toHaveBeenCalledWith(status({ address: 'replacement' }));
    expect(activeSettled).toBe(true);
    expect(trailingSettled).toBe(true);
    poller.dispose();
  });

  it('settles active and trailing callers when disposed during a hung request', async () => {
    backend.GetAPIStatus.mockReturnValueOnce(new Promise(() => {}));
    const poller = new ApiStatusPoller(makeHost());

    let activeSettled = false;
    let trailingSettled = false;
    void poller.refresh().then(() => { activeSettled = true; });
    void poller.refresh().then(() => { trailingSettled = true; });

    poller.dispose();
    await Promise.resolve();

    expect(activeSettled).toBe(true);
    expect(trailingSettled).toBe(true);
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

  it('does not restart or issue an explicit refresh after disposal', async () => {
    backend.GetAPIStatus.mockResolvedValue(status());
    const poller = new ApiStatusPoller(makeHost());

    poller.dispose();
    await poller.refresh();
    await poller.start();

    expect(backend.GetAPIStatus).not.toHaveBeenCalled();
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
