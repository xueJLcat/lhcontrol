import { afterEach, describe, expect, it, vi } from 'vitest';

const bindings = vi.hoisted(() => ({
  IsScanning: vi.fn(), GetScanStatus: vi.fn(), GetCurrentStationInfo: vi.fn(),
  CheckAllStationStatuses: vi.fn()
}));
vi.mock('../../wailsjs/go/main/App', () => bindings);
import { IsScanning, GetScanStatus, GetCurrentStationInfo, CheckAllStationStatuses } from './backend';

afterEach(() => { vi.useRealTimers(); vi.resetAllMocks(); });

describe('backend read deadlines', () => {
  it('returns successful snapshots and clears their timeout', async () => {
    vi.useFakeTimers();
    bindings.IsScanning.mockResolvedValue(true);
    expect(await IsScanning()).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('preserves synchronous bridge failures and clears their timeout', async () => {
    vi.useFakeTimers();
    const failure = new Error('bridge unavailable');
    bindings.IsScanning.mockImplementation(() => { throw failure; });
    await expect(IsScanning()).rejects.toBe(failure);
    expect(vi.getTimerCount()).toBe(0);
  });
  it.each([
    ['IsScanning', IsScanning], ['GetScanStatus', GetScanStatus],
    ['GetCurrentStationInfo', GetCurrentStationInfo]
  ] as const)('bounds a hung %s and ignores its late rejection', async (name, read) => {
    vi.useFakeTimers();
    let reject!: (error: Error) => void;
    bindings[name].mockReturnValue(new Promise((_, fail) => { reject = fail; }));
    const settled = vi.fn();
    const pending = read().then(settled, settled);
    await vi.advanceTimersByTimeAsync(10_000);
    expect(settled).toHaveBeenCalledOnce();
    expect(settled.mock.calls[0][0]).toBeInstanceOf(Error);
    reject(new Error('late failure'));
    await pending;
    expect(settled).toHaveBeenCalledOnce();
  });

  it('allows a configured long BLE refresh but bounds a hung refresh', async () => {
    vi.useFakeTimers();
    bindings.CheckAllStationStatuses.mockReturnValue(new Promise(() => {}));
    const settled = vi.fn();
    void CheckAllStationStatuses().then(settled, settled);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(settled).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(settled).toHaveBeenCalledOnce();
  });
});
