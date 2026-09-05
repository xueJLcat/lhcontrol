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
