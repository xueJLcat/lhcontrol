import { locale, t, type TranslationKey } from './i18n.svelte';

// Maps backend result strings (power-skip reasons, sentinel errors, scan and
// channel warnings, config warnings) onto translated copy. The exact-match
// keys are the backend constants verbatim, so under the English locale the
// output is byte-identical to the previous raw strings; zh-CN gets real
// translations. Anything unrecognized is returned unchanged so diagnostics
// are never lost.

const EXACT: ReadonlyMap<string, TranslationKey> = new Map([
  // Bulk skip reasons (internal/station/types.go Reason* constants).
  ['bulk operation timed out', 'bulk operation timed out'],
  ['station operation timed out', 'station operation timed out'],
  ['operation cancelled', 'operation cancelled'],
  ['application is shutting down', 'application is shutting down'],
  ['station is booting', 'station is booting'],
  ['already at target state', 'already at target state'],
  ['power control is not supported', 'power control is not supported'],
  ['standby is not supported', 'standby is not supported'],
  // Result-contract sentinel errors.
  ['another Bluetooth operation is already in progress', 'another Bluetooth operation is already in progress'],
  ['another Bluetooth operation is in progress', 'another Bluetooth operation is in progress'],
  ['Bluetooth scan is already active', 'Bluetooth scan is already active'],
  ['station not found', 'station not found'],
  ['channel conflicts with another visible station', 'channel conflicts with another visible station'],
  ['a recent successful scan is required', 'a recent successful scan is required'],
  ['station is transitioning between power states', 'station is transitioning between power states'],
  // Auto-sleep lifecycle event errors.
  ['cancelled before power commands were sent', 'cancelled before power commands were sent'],
  ['cancelled after scanning and before power commands were sent', 'cancelled after scanning and before power commands were sent'],
  ['watched process restarted or automatic sleep was reconfigured', 'watched process restarted or automatic sleep was reconfigured'],
  ['bulk power timeout reached', 'bulk power timeout reached'],
  // Fixed channel-operation warnings.
  ['One or more visible stations have an unknown channel; conflicts cannot be fully verified.',
    'One or more visible stations have an unknown channel; conflicts cannot be fully verified.'],
  ['The channel command was sent, but its result could not be confirmed.',
    'The channel command was sent, but its result could not be confirmed.']
]);

// Capability names used by UnsupportedCapabilityError ("X is not supported").
const CAPABILITY_ZH: Record<string, string> = {
  'power control': '电源控制',
  'standby': '待机',
  'characteristic write': '特征值写入',
  'power confirmation read': '电源确认读取',
  'power read': '电源状态读取',
  'channel read': '频道读取',
  'channel write': '频道写入',
  'identify': '识别',
  'safe channel control': '安全频道控制'
};

// Setting names used by the config validation errors. Unmapped names fall
// back to the English subject so messages stay readable.
const SETTINGS_SUBJECT_ZH: Record<string, string> = {
  'scan duration': '扫描时长',
  'status poll interval': '状态轮询间隔',
  'bulk power timeout': '批量电源操作超时',
  'station operation timeout': '单站操作超时',
  'per-station operation timeout': '单站操作超时',
  'power-on confirmation attempts': '开机确认次数',
  'power-off confirmation attempts': '待机/休眠确认次数',
  'power confirmation poll interval': '电源确认轮询间隔',
  'boot fallback window': '启动回退窗口',
  'sleep final write timeout': '休眠最终写入超时',
  'sleep prepare gap': '休眠预备间隔',
  'discovery attempts': '发现尝试次数',
  'discovery retry delay': '发现重试延迟',
  'recovery retry base': '恢复退避基底',
  'recovery retry maximum': '恢复退避上限',
  'initial read timeout': '初始读取超时',
  'scan read phase timeout': '扫描读取阶段超时',
  'status read timeout': '状态读取超时',
  'status refresh timeout': '状态刷新超时',
  'power write attempts': '电源写入次数',
  'operation retry delay': '操作重试延迟',
  'channel confirmation attempts': '频道确认次数',
  'channel confirmation interval': '频道确认间隔',
  'confirmation reconnect threshold': '确认重连阈值',
  'confirmation reconnect delay': '确认重连延迟',
  'identify attempts': '识别尝试次数',
  'presence miss threshold': '在场漏扫阈值'
};

interface CopyPattern {
  pattern: RegExp;
  render(match: RegExpMatchArray): string;
}

// Parameterized backend strings. More specific patterns come first.
const PATTERNS: readonly CopyPattern[] = [
  {
    // station/scan.go: connections not released before scanning.
    pattern: /^(\d+) station connection\(s\) could not be fully released before scanning: (.+)$/s,
    render: (m) => t('scan.warning.connectionsNotReleased', { count: m[1], detail: backendCopy(m[2]) })
  },
  {
    // station/scan.go: initial read failures after discovery.
    pattern: /^(\d+) station\(s\) were discovered, but some initial values could not be read: (.+)$/s,
    render: (m) => t('scan.warning.initialReadFailures', { count: m[1], detail: backendCopy(m[2]) })
  },
  {
    // bluetooth/channel.go: final-readback confirmation warning.
    pattern: /^channel (\d+) was confirmed by the final readback$/,
    render: (m) => t('channel.warning.confirmedByFinalReadback', { channel: m[1] })
  },
  {
    // bluetooth/channel.go: write reported not sent but readback observed it.
    pattern: /^the write was reported as not sent, but channel (\d+) was observed by readback: (.+)$/s,
    render: (m) => t('channel.warning.notSentButObserved', { channel: m[1], detail: backendCopy(m[2]) })
  },
  {
    // bluetooth/channel.go: write errored but readback confirmed it.
    pattern: /^the write call reported an error, but channel (\d+) was confirmed by readback: (.+)$/s,
    render: (m) => t('channel.warning.errorButConfirmed', { channel: m[1], detail: backendCopy(m[2]) })
  },
  {
    // app.go config-persistence warnings.
    pattern: /^Configuration could not be loaded: (.+)$/s,
    render: (m) => t('Configuration could not be loaded: {detail}', { detail: backendCopy(m[1]) })
  },
  {
    pattern: /^Configuration changes could not be saved: (.+)$/s,
    render: (m) => t('Configuration changes could not be saved: {detail}', { detail: backendCopy(m[1]) })
  },
  {
    // UnsupportedCapabilityError shape: "<capability> is not supported[: cause]".
    pattern: /^([A-Za-z ]+?) is not supported(?:$|:)/,
    render: (m) => {
      const capability = locale() === 'zh-CN'
        ? (CAPABILITY_ZH[m[1]] ?? m[1])
        : m[1];
      return t('{capability} is not supported', { capability });
    }
  },
  {
    // Config setters: "<subject> must be between <min> and <max>[ unit], got <value>".
    pattern: /^(.+?) must be between (\d+) and (\d+)(?: (seconds|milliseconds))?, got (\d+)$/,
    render: (m) => {
      if (locale() !== 'zh-CN') {
        return `${m[1]} must be between ${m[2]} and ${m[3]}${m[4] ? ` ${m[4]}` : ''}, got ${m[5]}`;
      }
      const unit = m[4] === 'milliseconds' ? '毫秒' : m[4] === 'seconds' ? '秒' : '';
      return t('settings.error.between', {
        subject: SETTINGS_SUBJECT_ZH[m[1]] ?? m[1],
        min: m[2],
        max: m[3],
        unit,
        value: m[5]
      });
    }
  },
  {
    // Config setters: "<subject> must cover/must not exceed/cannot exceed
    // <other> of <limit> seconds, got <value>".
    pattern: /^(.+?) (must cover the|must not exceed the|cannot exceed the) (.+?) of (\d+) seconds, got (\d+)$/,
    render: (m) => {
      if (locale() !== 'zh-CN') {
        return `${m[1]} ${m[2]} ${m[3]} of ${m[4]} seconds, got ${m[5]}`;
      }
      const subject = SETTINGS_SUBJECT_ZH[m[1]] ?? m[1];
      const other = SETTINGS_SUBJECT_ZH[m[3]] ?? m[3];
      const key = m[2] === 'must cover the'
        ? 'settings.error.mustCover'
        : 'settings.error.mustNotExceed';
      return t(key, { subject, other, limit: m[4], value: m[5] });
    }
  }
];

export function backendCopy(raw: string | null | undefined): string {
  if (raw === null || raw === undefined) return '';
  const trimmed = raw.trim();
  if (trimmed === '') return '';
  const exact = EXACT.get(trimmed);
  if (exact) return t(exact);
  for (const { pattern, render } of PATTERNS) {
    const match = trimmed.match(pattern);
    if (match) return render(match);
  }
  return raw;
}

// Translate a possibly-empty backend string, falling back to translated copy
// when there is nothing to translate (for "reason || not actionable" sites).
export function backendCopyOr(raw: string | null | undefined, fallback: TranslationKey): string {
  const value = backendCopy(raw);
  return value === '' ? t(fallback) : value;
}

// Join translated backend warnings; zh-CN uses the fullwidth semicolon so
// concatenated sentences stay separable.
export function joinBackendCopy(list: readonly string[] | null | undefined): string {
  const items = (list ?? []).filter(Boolean).map((item) => backendCopy(item));
  return items.join(locale() === 'zh-CN' ? '；' : ' ');
}
