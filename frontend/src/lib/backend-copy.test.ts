import { afterEach, describe, expect, it } from 'vitest';

import { backendCopy, backendCopyOr, joinBackendCopy } from './backend-copy';
import { setLanguagePreference } from './i18n.svelte';

describe('backendCopy', () => {
  afterEach(() => {
    setLanguagePreference('en');
  });

  it('returns empty backend values unchanged', () => {
    expect(backendCopy('')).toBe('');
    expect(backendCopy(null)).toBe('');
    expect(backendCopy(undefined)).toBe('');
  });

  it('passes through unrecognized strings so diagnostics survive', () => {
    const raw = 'WinRT GATT error 0x80650003 while reading characteristic';
    expect(backendCopy(raw)).toBe(raw);
  });

  it('keeps English output byte-identical for known constants', () => {
    expect(backendCopy('station is booting')).toBe('station is booting');
    expect(backendCopy('bulk operation timed out')).toBe('bulk operation timed out');
    expect(backendCopy('another Bluetooth operation is in progress'))
      .toBe('another Bluetooth operation is in progress');
  });

  it('translates known constants under zh-CN', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('station is booting')).toBe('基站正在启动');
    expect(backendCopy('operation cancelled')).toBe('操作已取消');
    expect(backendCopy('another Bluetooth operation is in progress')).toBe('另一个蓝牙操作正在进行');
  });

  it('translates scan warning templates and keeps nested detail', () => {
    setLanguagePreference('zh-CN');
    const raw = '2 station(s) were discovered, but some initial values could not be read: some transport failure';
    expect(backendCopy(raw)).toBe('已发现 2 个基站，但部分初始值读取失败：some transport failure');
  });

  it('translates channel readback warnings', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('channel 4 was confirmed by the final readback')).toBe('频道 4 已由最终回读确认');
  });

  it('translates unsupported capability reasons', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('standby is not supported')).toBe('不支持待机');
    expect(backendCopy('power confirmation read is not supported: ATT error 0x06'))
      .toBe('不支持电源确认读取');
  });

  it('keeps unmatched capability names readable', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('mystery feature is not supported')).toBe('不支持mystery feature');
  });

  it('falls back to translated copy when the backend string is empty', () => {
    expect(backendCopyOr('', 'not actionable')).toBe('not actionable');
    setLanguagePreference('zh-CN');
    expect(backendCopyOr(null, 'not actionable')).toBe('不可操作');
  });

  it('keeps config range rejections byte-identical in English', () => {
    const raw = 'scan duration must be between 2 and 30 seconds, got 1';
    expect(backendCopy(raw)).toBe(raw);
    const generic = 'power write attempts must be between 1 and 5, got 9';
    expect(backendCopy(generic)).toBe(generic);
  });

  it('translates config range rejections under zh-CN', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('scan duration must be between 2 and 30 seconds, got 1'))
      .toBe('扫描时长必须在 2–30 秒范围内，当前为 1');
    expect(backendCopy('power write attempts must be between 1 and 5, got 9'))
      .toBe('电源写入次数必须在 1–5 范围内，当前为 9');
  });

  it('translates cross-field config rejections under zh-CN', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('bulk power timeout must cover the per-station operation timeout of 30 seconds, got 20'))
      .toBe('批量电源操作超时必须不小于单站操作超时（30 秒），当前为 20');
    expect(backendCopy('station operation timeout cannot exceed the bulk power timeout of 120 seconds, got 130'))
      .toBe('单站操作超时不能超过批量电源操作超时（120 秒），当前为 130');
    expect(backendCopy('initial read timeout must not exceed the scan read phase timeout of 60 seconds, got 70'))
      .toBe('初始读取超时不能超过扫描读取阶段超时（60 秒），当前为 70');
  });

  it('keeps cross-field config rejections byte-identical in English', () => {
    const raw = 'bulk power timeout must cover the per-station operation timeout of 30 seconds, got 20';
    expect(backendCopy(raw)).toBe(raw);
  });

  it('joins translated warnings with a locale-appropriate separator', () => {
    const warnings = ['station is booting', 'unknown detail stays raw'];
    expect(joinBackendCopy(warnings)).toBe('station is booting unknown detail stays raw');
    setLanguagePreference('zh-CN');
    expect(joinBackendCopy(warnings)).toBe('基站正在启动；unknown detail stays raw');
    expect(joinBackendCopy([])).toBe('');
    expect(joinBackendCopy(null)).toBe('');
  });
});
