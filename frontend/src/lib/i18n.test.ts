import { afterEach, describe, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  GetLanguage: vi.fn(),
  SetLanguage: vi.fn()
}));
vi.mock('./backend', () => backend);
import {
  languagePreference, locale, localeFromLanguages, onLocaleApplied,
  saveLanguagePreference, setLanguagePreference, setLocale, t
} from './i18n.svelte';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

afterEach(() => {
  vi.useRealTimers();
  vi.resetAllMocks();
  setLanguagePreference('system');
});

describe('i18n', () => {
  it('detects Simplified Chinese from the system language list', () => {
    expect(localeFromLanguages(['zh-CN', 'en-US'])).toBe('zh-CN');
    expect(localeFromLanguages(['en-US'])).toBe('en');
  });

  it('uses the first supported system language in preference order', () => {
    expect(localeFromLanguages(['en-US', 'zh-CN'])).toBe('en');
    expect(localeFromLanguages(['fr-FR', 'zh-CN', 'en-US'])).toBe('zh-CN');
    expect(localeFromLanguages(['fr-FR', 'de-DE'])).toBe('en');
  });

  it('tracks the persisted preference separately from the effective locale', () => {
    setLanguagePreference('zh-CN');
    expect(languagePreference()).toBe('zh-CN');
    expect(locale()).toBe('zh-CN');
    setLanguagePreference('system');
    expect(languagePreference()).toBe('system');
  });

  it('switches translations immediately and interpolates values', () => {
    setLocale('zh-CN');
    expect(t('Settings')).toBe('设置');
    expect(t('Channel {channel}', { channel: 8 })).toBe('频道 8');
    expect(t('raw {value}', { value: '0x0B' })).toBe('原始值 0x0B');
    expect(document.documentElement.lang).toBe('zh-CN');
    setLocale('en');
    expect(t('Settings')).toBe('Settings');
    expect(t('raw {value}', { value: '0x0B' })).toBe('raw 0x0B');
  });
});

describe('language persistence', () => {
  it('bounds a hung SetLanguage binding and reports the failed save', async () => {
    vi.useFakeTimers();
    backend.SetLanguage.mockReturnValue(new Promise(() => {}));

    const save = saveLanguagePreference('zh-CN');
    await vi.advanceTimersByTimeAsync(10_000);
    const result = await save;

    if (result.saved) {
      throw new Error('expected the hung language save to fail');
    }
    expect(String(result.error)).toContain('Language save timed out');
    // The timed-out save reverted the applied preference; a later save must
    // still run instead of queueing forever behind the hung call.
    backend.SetLanguage.mockResolvedValue(undefined);
    const retry = await saveLanguagePreference('zh-CN');
    expect(retry.saved).toBe(true);
    expect(backend.SetLanguage).toHaveBeenCalledWith('zh-CN');
  });

  it('applies a language write that succeeds after its watchdog timed out', async () => {
    vi.useFakeTimers();
    let persisted = '';
    const releaseSave = deferred<void>();
    backend.GetLanguage.mockImplementation(async () => persisted);
    backend.SetLanguage.mockImplementation(async (value: string) => {
      await releaseSave.promise;
      persisted = value;
    });

    const save = saveLanguagePreference('zh-CN');
    await vi.advanceTimersByTimeAsync(10_000);
    expect((await save).saved).toBe(false);
    expect(languagePreference()).toBe('system');

    releaseSave.resolve(undefined);
    await vi.advanceTimersByTimeAsync(0);
    expect(persisted).toBe('zh-CN');
    expect(languagePreference()).toBe('zh-CN');
    expect(locale()).toBe('zh-CN');
  });

  it('uses the persisted language when an older timed-out write overwrites a newer save', async () => {
    vi.useFakeTimers();
    let persisted = '';
    const releaseFirst = deferred<void>();
    backend.GetLanguage.mockImplementation(async () => persisted);
    backend.SetLanguage.mockImplementation(async (value: string) => {
      if (value === 'zh-CN') await releaseFirst.promise;
      persisted = value;
    });

    const first = saveLanguagePreference('zh-CN');
    await vi.advanceTimersByTimeAsync(10_000);
    expect((await first).saved).toBe(false);
    expect((await saveLanguagePreference('en')).saved).toBe(true);
    expect(persisted).toBe('en');

    releaseFirst.resolve(undefined);
    await vi.advanceTimersByTimeAsync(0);
    expect(persisted).toBe('zh-CN');
    expect(languagePreference()).toBe('zh-CN');
  });

  it('does not replace a new language choice with a late reconciliation read', async () => {
    vi.useFakeTimers();
    let persisted = '';
    const releaseFirst = deferred<void>();
    const releaseRead = deferred<string>();
    backend.GetLanguage.mockReturnValue(releaseRead.promise);
    backend.SetLanguage.mockImplementation(async (value: string) => {
      if (value === 'zh-CN') await releaseFirst.promise;
      persisted = value;
    });

    const first = saveLanguagePreference('zh-CN');
    await vi.advanceTimersByTimeAsync(10_000);
    expect((await first).saved).toBe(false);
    releaseFirst.resolve(undefined);
    await vi.advanceTimersByTimeAsync(0);
    expect(backend.GetLanguage).toHaveBeenCalledOnce();

    const next = saveLanguagePreference('en');
    expect(languagePreference()).toBe('en');
    releaseRead.resolve(persisted);
    await vi.advanceTimersByTimeAsync(0);
    expect((await next).saved).toBe(true);
    expect(persisted).toBe('en');
    expect(languagePreference()).toBe('en');
  });
});

describe('applied-locale notifications', () => {
  it('notifies subscribers when the applied locale actually changes', () => {
    const applied: string[] = [];
    const stop = onLocaleApplied((next) => applied.push(next));

    setLanguagePreference('zh-CN');
    // Re-applying the same preference or an identical locale stays silent.
    setLanguagePreference('zh-CN');
    setLocale('zh-CN');

    stop();
    setLanguagePreference('system');

    expect(applied).toEqual(['zh-CN']);
  });
});
