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

  it('waits for an earlier instance save before a reopened instance loads', async () => {
    let persisted = 5;
    const releaseSave = deferred<void>();
    const getter = vi.fn(async () => persisted);
    const setter = vi.fn(async (value: number) => {
      await releaseSave.promise;
      persisted = value;
    });
    const options = {
      getter,
      setter,
      loadMessage: 'Scan duration could not be loaded' as const,
      saveMessage: 'Scan duration could not be saved' as const
    };
    const original = new AsyncSetting(options);
    const reopened = new AsyncSetting(options);

    await original.load();
    const save = original.change(10);
    await vi.waitFor(() => expect(setter).toHaveBeenCalledOnce());

    const load = reopened.load();
    await Promise.resolve();
    const getterCallsWhileSaving = getter.mock.calls.length;
    const reopenedBusyWhileSaving = reopened.busy;

    releaseSave.resolve(undefined);
    await Promise.all([save, load]);

    expect(getterCallsWhileSaving).toBe(1);
    expect(reopenedBusyWhileSaving).toBe(true);
    expect(reopened.value).toBe(10);
  });

  it('serializes saves and their follow-ups across instances', async () => {
    const releaseFirstSave = deferred<void>();
    const setter = vi.fn(async (value: number) => {
      if (value === 10) await releaseFirstSave.promise;
    });
    const applied: number[] = [];
    const options = {
      getter: async () => 5,
      setter,
      afterSave: (value: number) => {
        applied.push(value);
      },
      loadMessage: 'Scan duration could not be loaded' as const,
      saveMessage: 'Scan duration could not be saved' as const
    };
    const first = new AsyncSetting(options);
    const second = new AsyncSetting(options);
    await Promise.all([first.load(), second.load()]);

    const firstSave = first.change(10);
    await vi.waitFor(() => expect(setter).toHaveBeenCalledOnce());
    const secondSave = second.change(20);
    await Promise.resolve();
    const callsBeforeRelease = setter.mock.calls.length;

    releaseFirstSave.resolve(undefined);
    await Promise.all([firstSave, secondSave]);

    expect(callsBeforeRelease).toBe(1);
    expect(setter.mock.calls.map(([value]) => value)).toEqual([10, 20]);
    expect(applied).toEqual([10, 20]);
  });
});
