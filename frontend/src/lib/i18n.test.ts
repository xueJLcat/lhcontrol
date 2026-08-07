import { afterEach, describe, expect, it } from 'vitest';
import {
  languagePreference, locale, localeFromLanguages, setLanguagePreference, setLocale, t
} from './i18n.svelte';

afterEach(() => setLanguagePreference('system'));

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
    expect(document.documentElement.lang).toBe('zh-CN');
    setLocale('en');
    expect(t('Settings')).toBe('Settings');
  });
});
