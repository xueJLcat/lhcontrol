import { afterEach, describe, expect, it } from 'vitest';
import { localeFromLanguages, setLocale, t } from './i18n.svelte';

afterEach(() => setLocale('en'));

describe('i18n', () => {
  it('detects Simplified Chinese from the system language list', () => {
    expect(localeFromLanguages(['zh-CN', 'en-US'])).toBe('zh-CN');
    expect(localeFromLanguages(['en-US'])).toBe('en');
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
