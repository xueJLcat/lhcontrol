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

  it('keeps power confirmation errors byte-identical in English', () => {
    const raw = 'on command sent but state confirmation failed (actual booting, raw 0x01): read power characteristic: connection reset';
    expect(backendCopy(raw)).toBe(raw);
  });

  it('translates power confirmation errors under zh-CN', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('sleep command sent but state confirmation failed (actual on, raw 0x0B): timeout'))
      .toBe('休眠命令已发送，但状态确认失败（实际 开启，原始值 0x0B）：timeout');
  });

  it('translates aggregate read failures line by line under zh-CN', () => {
    setLanguagePreference('zh-CN');
    const raw = 'station status read was incomplete: read power characteristic: boom\nread channel characteristic: kaput';
    expect(backendCopy(raw)).toBe('基站状态读取不完整：读取电源特征值：boom；读取频道特征值：kaput');
    expect(backendCopy('initial station read was incomplete: read power characteristic: boom'))
      .toBe('初始读取不完整：读取电源特征值：boom');
  });

  it('translates transport operation prefixes under zh-CN and recurses into the cause', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('read power characteristic: some WinRT failure'))
      .toBe('读取电源特征值：some WinRT failure');
    expect(backendCopy('connect station: station is booting')).toBe('连接基站：基站正在启动');
  });

  it('keeps transport operation prefixes byte-identical in English', () => {
    const raw = 'discover GATT services: the radio is gone';
    expect(backendCopy(raw)).toBe(raw);
  });

  it('does not parse ordinary detail text as a transport operation', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('mystery text: with a colon')).toBe('mystery text: with a colon');
  });

  it('translates per-station scan warning details line by line under zh-CN', () => {
    setLanguagePreference('zh-CN');
    const raw = '2 station(s) were discovered, but some initial values could not be read: AA:BB:CC:DD:EE:01: station is booting\nAA:BB:CC:DD:EE:02: station is booting';
    expect(backendCopy(raw))
      .toBe('已发现 2 个基站，但部分初始值读取失败：AA:BB:CC:DD:EE:01: 基站正在启动；AA:BB:CC:DD:EE:02: 基站正在启动');
  });

  it('keeps scan and channel warning templates byte-identical in English', () => {
    const connections = '2 station connection(s) could not be fully released before scanning: AA:BB:CC:DD:EE:01: boom';
    expect(backendCopy(connections)).toBe(connections);
    const reads = '2 station(s) were discovered, but some initial values could not be read: AA:BB:CC:DD:EE:01: boom';
    expect(backendCopy(reads)).toBe(reads);
    expect(backendCopy('channel 4 was confirmed by the final readback'))
      .toBe('channel 4 was confirmed by the final readback');
    const notSent = 'the write was reported as not sent, but channel 5 was observed by readback: detail';
    expect(backendCopy(notSent)).toBe(notSent);
    const errored = 'the write call reported an error, but channel 5 was confirmed by readback: detail';
    expect(backendCopy(errored)).toBe(errored);
  });

  it('keeps wrapped sentinel errors byte-identical in English', () => {
    const booting = 'station is booting; retry after transition: station is transitioning between power states';
    expect(backendCopy(booting)).toBe(booting);
    expect(backendCopy('station not found: AA:BB:CC:DD:EE:01')).toBe('station not found: AA:BB:CC:DD:EE:01');
    const conflict = 'channel conflicts with another visible station: channel 4 is used by LHB-A (AA:BB:CC:DD:EE:01)';
    expect(backendCopy(conflict)).toBe(conflict);
    expect(backendCopy('a recent successful scan is required before changing a channel'))
      .toBe('a recent successful scan is required before changing a channel');
    expect(backendCopy('operation is not supported: standby is unavailable'))
      .toBe('operation is not supported: standby is unavailable');
  });

  it('translates wrapped sentinel errors under zh-CN', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('station is booting; retry after transition: station is transitioning between power states'))
      .toBe('基站正在启动中，请待状态切换完成后重试：基站正在电源状态切换中');
    expect(backendCopy('station is booting; retry channel change after transition: station is transitioning between power states'))
      .toBe('基站正在启动中，请待状态切换完成后重试频道修改：基站正在电源状态切换中');
    expect(backendCopy('station not found: AA:BB:CC:DD:EE:01')).toBe('未找到基站：AA:BB:CC:DD:EE:01');
    expect(backendCopy('station not found: station AA:BB:CC:DD:EE:01 was not seen in the latest scan'))
      .toBe('未找到基站：基站 AA:BB:CC:DD:EE:01 未在最近一次扫描中发现');
    expect(backendCopy('channel conflicts with another visible station: channel 4 is used by LHB-A (AA:BB:CC:DD:EE:01)'))
      .toBe('频道与另一个可见基站冲突：频道 4 已被 LHB-A（AA:BB:CC:DD:EE:01）使用');
    expect(backendCopy('a recent successful scan is required before changing a channel'))
      .toBe('更改频道前需要先完成一次有效扫描');
    expect(backendCopy('a recent successful scan is required: one or more visible stations have an unknown channel'))
      .toBe('需要先完成一次有效扫描：一个或多个可见基站的频道未知');
  });

  it('translates unsupported-operation errors without misparsing the capability pattern', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('operation is not supported')).toBe('不支持该操作');
    expect(backendCopy('operation is not supported: standby is unavailable')).toBe('不支持该操作：待机不可用');
    expect(backendCopy('operation is not supported: safe channel changes require read and write support'))
      .toBe('不支持该操作：安全频道修改需要读写支持');
  });

  it('translates the fall-below cross-field rejection under zh-CN', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('recovery retry maximum must not fall below the recovery retry base of 30 seconds, got 10'))
      .toBe('恢复退避上限不得低于恢复退避基底（30 秒），当前为 10');
  });

  it('translates the recovery and freshness range subjects under zh-CN', () => {
    setLanguagePreference('zh-CN');
    expect(backendCopy('absent station retry limit must be between 1 and 20, got 0'))
      .toBe('失联站放弃前重试数必须在 1–20 范围内，当前为 0');
    expect(backendCopy('channel scan freshness must be between 30 and 600 seconds, got 1'))
      .toBe('通道扫描新鲜窗口必须在 30–600 秒范围内，当前为 1');
    expect(backendCopy('Bluetooth init retry must be between 1 and 30 seconds, got 99'))
      .toBe('适配器初始化冷却必须在 1–30 秒范围内，当前为 99');
  });

  it('joins translated warnings with a locale-appropriate separator', () => {
    const warnings = ['station is booting', 'unknown detail stays raw'];
    expect(joinBackendCopy(warnings)).toBe('station is booting unknown detail stays raw');
    setLanguagePreference('zh-CN');
    expect(joinBackendCopy(warnings)).toBe('基站正在启动；unknown detail stays raw');
    expect(joinBackendCopy([])).toBe('');
    expect(joinBackendCopy(null)).toBe('');
  });

  // Guards against a template/argument mismatch in any translated backend
  // string: a missing or misspelled placeholder leaks a literal "{name}" into
  // the rendered copy. Every pattern branch must produce output free of
  // unfilled "{...}" markers under zh-CN.
  it('leaves no unfilled placeholders in any translated pattern under zh-CN', () => {
    setLanguagePreference('zh-CN');
    const patternInputs = [
      '2 station connection(s) could not be fully released before scanning: AA:BB:CC:DD:EE:01: boom',
      '2 station(s) were discovered, but some initial values could not be read: AA:BB:CC:DD:EE:01: boom',
      'on command sent but state confirmation failed (actual booting, raw 0x01): timeout',
      'station status read was incomplete: read power characteristic: boom',
      'initial station read was incomplete: read power characteristic: boom',
      'channel 4 was confirmed by the final readback',
      'the write was reported as not sent, but channel 5 was observed by readback: detail',
      'the write call reported an error, but channel 5 was confirmed by readback: detail',
      'Configuration could not be loaded: boom',
      'Configuration changes could not be saved: boom',
      'Configuration was reset to defaults during recovery: boom',
      'identify is not supported',
      'identify is not supported: ATT error',
      'scan duration must be between 2 and 30 seconds, got 1',
      'power write attempts must be between 1 and 5, got 9',
      'bulk power timeout must cover the per-station operation timeout of 30 seconds, got 20',
      'station operation timeout cannot exceed the bulk power timeout of 120 seconds, got 130',
      'initial read timeout must not exceed the station operation timeout of 30 seconds, got 60',
      'recovery retry maximum must not fall below the recovery retry base of 30 seconds, got 10',
      'station is booting; retry after transition: station is transitioning between power states',
      'station is booting; retry channel change after transition: station is transitioning between power states',
      'station not found: AA:BB:CC:DD:EE:01',
      'station not found: station AA:BB:CC:DD:EE:01 was not seen in the latest scan',
      'channel conflicts with another visible station: channel 4 is used by LHB-A (AA:BB:CC:DD:EE:01)',
      'operation is not supported',
      'operation is not supported: standby is unavailable',
      'read power characteristic: boom'
    ];
    for (const input of patternInputs) {
      const output = backendCopy(input);
      expect(output, `input: ${input}`).not.toContain('{');
    }
  });
});
