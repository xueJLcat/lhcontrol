import { describe, expect, it, vi } from 'vitest';

vi.mock('../../toast', () => ({ pushToast: vi.fn() }));

import { AsyncSetting } from './async-setting.svelte';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function createSetting(getter: () => Promise<number>, setter = vi.fn(async () => {})) {
  return {
    setting: new AsyncSetting({
      getter,
      setter,
      loadMessage: 'Scan duration could not be loaded',
      saveMessage: 'Scan duration could not be saved'
    }),
    setter
  };
}

describe('AsyncSetting', () => {
  it('marks a load busy and coalesces overlapping retries', async () => {
    const pending = deferred<number>();
    const getter = vi.fn(() => pending.promise);
    const { setting } = createSetting(getter);

    const first = setting.load();
    const duplicate = setting.load();

    expect(setting.busy).toBe(true);
    expect(getter).toHaveBeenCalledOnce();

    pending.resolve(12);
    await Promise.all([first, duplicate]);

    expect(setting.value).toBe(12);
    expect(setting.busy).toBe(false);
  });

  it('does not save while a refresh can still replace the displayed value', async () => {
    const pending = deferred<number>();
    const getter = vi.fn()
      .mockResolvedValueOnce(5)
      .mockImplementationOnce(() => pending.promise);
    const { setting, setter } = createSetting(getter);
    await setting.load();

    const refresh = setting.load();
    await setting.change(20);

    expect(setter).not.toHaveBeenCalled();
    expect(setting.value).toBe(5);

    pending.resolve(8);
    await refresh;
    expect(setting.value).toBe(8);
  });
});
