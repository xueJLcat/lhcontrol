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
  ['station is busy', 'station is busy'],
  // Result-contract sentinel errors.
  ['another Bluetooth operation is already in progress', 'another Bluetooth operation is already in progress'],
  ['another Bluetooth operation is in progress', 'another Bluetooth operation is in progress'],
  ['Bluetooth scan is already active', 'Bluetooth scan is already active'],
  ['station not found', 'station not found'],
  ['channel conflicts with another visible station', 'channel conflicts with another visible station'],
  ['a recent successful scan is required', 'a recent successful scan is required'],
  ['a recent successful scan is required before changing a channel', 'a recent successful scan is required before changing a channel'],
  ['a recent successful scan is required: one or more visible stations have an unknown channel',
    'a recent successful scan is required: one or more visible stations have an unknown channel'],
  ['station is transitioning between power states', 'station is transitioning between power states'],
  // ErrUnsupported detail halves (internal/station power/channel/capability
  // paths) surfaced inside "operation is not supported: <detail>".
  ['power write is unavailable', 'power write is unavailable'],
  ['standby is unavailable', 'standby is unavailable'],
  ['identify is unavailable', 'identify is unavailable'],
  ['safe channel changes require read and write support', 'safe channel changes require read and write support'],
  // Auto-sleep lifecycle event errors.
  ['cancelled before power commands were sent', 'cancelled before power commands were sent'],
  ['cancelled after scanning and before power commands were sent', 'cancelled after scanning and before power commands were sent'],
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

// Power state names used by PowerConfirmationError ("actual <state>").
const POWER_STATE_ZH: Record<string, string> = {
  on: '开启',
  standby: '待机',
  sleep: '休眠',
  booting: '启动中',
  unknown: '未知'
};

// Closed set of transport operation prefixes used by DeviceTransportError
// ("<operation>: <cause>"). Unmapped prefixes are not matched, so ordinary
// English detail text is never misparsed as an operation label.
const OPERATION_ZH: Record<string, string> = {
  'read power characteristic': '读取电源特征值',
  'read channel characteristic': '读取频道特征值',
  'write characteristic': '写入特征值',
  'write power characteristic': '写入电源特征值',
  'connect station': '连接基站',
  'retry station connection': '重试基站连接',
  'discover GATT services': '发现 GATT 服务',
  'discover control characteristics': '发现控制特征值',
  'disconnect stale station connection': '断开过期的基站连接',
  'cleanup before discovery retry': '发现重试前清理',
  'cleanup before capability refresh': '能力刷新前清理',
  'cleanup cancelled capability discovery': '清理已取消的能力发现',
  'cleanup cancelled initial read': '清理已取消的初始读取',
  'finish previous connection cleanup': '完成先前连接清理',
  'cleanup unsupported station connection': '清理不支持的基站连接'
};

const TRANSPORT_PREFIX_PATTERN = new RegExp(
  `^(${Object.keys(OPERATION_ZH).join('|')}): (.+)$`,
  's'
);

// errors.Join renders joined errors on separate lines. Aggregate messages
// embed such joins, so each line is mapped independently. Lines shaped like
// "<MAC>: <error>" (per-station scan read failures) keep their address and
// translate only the error half.
function mapJoinedLines(detail: string): string {
  return detail
    .split('\n')
    .map((line) => {
      const stationLine = line.match(/^([0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5}): (.+)$/s);
      if (stationLine) return `${stationLine[1]}: ${backendCopy(stationLine[2])}`;
      return backendCopy(line);
    })
    .join(locale() === 'zh-CN' ? '；' : '\n');
}

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
  'presence miss threshold': '在场漏扫阈值',
  'absent station retry limit': '失联站放弃前重试数',
  'channel scan freshness': '通道扫描新鲜窗口',
  'Bluetooth init retry': '适配器初始化冷却'
};

interface CopyPattern {
  pattern: RegExp;
  render(match: RegExpMatchArray): string;
}

// Parameterized backend strings. More specific patterns come first.
const PATTERNS: readonly CopyPattern[] = [
  {
    // station/scan.go: connections not released before scanning. The detail
    // is an errors.Join of "<MAC>: <error>" lines.
    pattern: /^(\d+) station connection\(s\) could not be fully released before scanning: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('scan.warning.connectionsNotReleased', { count: m[1], detail: mapJoinedLines(m[2]) })
      : `${m[1]} station connection(s) could not be fully released before scanning: ${mapJoinedLines(m[2])}`
  },
  {
    // station/scan.go: initial read failures after discovery. The detail is
    // an errors.Join of "<MAC>: <error>" lines.
    pattern: /^(\d+) station\(s\) were discovered, but some initial values could not be read: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('scan.warning.initialReadFailures', { count: m[1], detail: mapJoinedLines(m[2]) })
      : `${m[1]} station(s) were discovered, but some initial values could not be read: ${mapJoinedLines(m[2])}`
  },
  {
    // bluetooth/power.go PowerConfirmationError shape.
    pattern: /^(on|standby|sleep) command sent but state confirmation failed \(actual (on|standby|sleep|booting|unknown), raw (0x[0-9A-Fa-f]{2}|unavailable)\): (.+)$/s,
    render: (m) => {
      if (locale() !== 'zh-CN') {
        return `${m[1]} command sent but state confirmation failed (actual ${m[2]}, raw ${m[3]}): ${m[4]}`;
      }
      return t('power.confirmationError', {
        target: POWER_STATE_ZH[m[1]],
        actual: POWER_STATE_ZH[m[2]],
        raw: m[3],
        detail: backendCopy(m[4])
      });
    }
  },
  {
    // bluetooth/status.go aggregate read failures; the detail is an
    // errors.Join rendered on separate lines.
    pattern: /^station status read was incomplete: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('status.readIncomplete', { detail: mapJoinedLines(m[1]) })
      : `station status read was incomplete: ${m[1]}`
  },
  {
    pattern: /^initial station read was incomplete: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('scan.initialReadIncomplete', { detail: mapJoinedLines(m[1]) })
      : `initial station read was incomplete: ${m[1]}`
  },
  {
    // bluetooth DeviceTransportError shape with a known operation prefix.
    pattern: TRANSPORT_PREFIX_PATTERN,
    render: (m) => {
      if (locale() !== 'zh-CN') return `${m[1]}: ${m[2]}`;
      return `${OPERATION_ZH[m[1]]}：${backendCopy(m[2])}`;
    }
  },
  {
    // bluetooth/channel.go: final-readback confirmation warning.
    pattern: /^channel (\d+) was confirmed by the final readback$/,
    render: (m) => locale() === 'zh-CN'
      ? t('channel.warning.confirmedByFinalReadback', { channel: m[1] })
      : `channel ${m[1]} was confirmed by the final readback`
  },
  {
    // bluetooth/channel.go: write reported not sent but readback observed it.
    pattern: /^the write was reported as not sent, but channel (\d+) was observed by readback: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('channel.warning.notSentButObserved', { channel: m[1], detail: backendCopy(m[2]) })
      : `the write was reported as not sent, but channel ${m[1]} was observed by readback: ${backendCopy(m[2])}`
  },
  {
    // bluetooth/channel.go: write errored but readback confirmed it.
    pattern: /^the write call reported an error, but channel (\d+) was confirmed by readback: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('channel.warning.errorButConfirmed', { channel: m[1], detail: backendCopy(m[2]) })
      : `the write call reported an error, but channel ${m[1]} was confirmed by readback: ${backendCopy(m[2])}`
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
    // config persistence: blocked-save recovery quarantined the file and
    // fell back to defaults mid-session.
    pattern: /^Configuration was reset to defaults during recovery: (.+)$/s,
    render: (m) => t('Configuration was reset to defaults during recovery: {detail}', { detail: backendCopy(m[1]) })
  },
  {
    // station power paths: booting rejection wrapping the transition sentinel.
    pattern: /^station is booting; retry after transition: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('power.error.bootingRetry', { detail: backendCopy(m[1]) })
      : `station is booting; retry after transition: ${m[1]}`
  },
  {
    // station/channel.go: booting rejection for channel changes.
    pattern: /^station is booting; retry channel change after transition: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('channel.error.bootingRetry', { detail: backendCopy(m[1]) })
      : `station is booting; retry channel change after transition: ${m[1]}`
  },
  {
    // station_lookup.go: not-found sentinel with the address as detail.
    pattern: /^station not found: (.+)$/s,
    render: (m) => locale() === 'zh-CN'
      ? t('station.error.notFoundDetail', { detail: backendCopy(m[1]) })
      : `station not found: ${m[1]}`
  },
  {
    // station/channel.go: detail inside the not-found wrapper above.
    pattern: /^station (.+) was not seen in the latest scan$/,
    render: (m) => locale() === 'zh-CN'
      ? t('station.error.notSeenInLatestScan', { address: m[1] })
      : `station ${m[1]} was not seen in the latest scan`
  },
  {
    // station/channel.go: conflict sentinel with the occupant detail.
    pattern: /^channel conflicts with another visible station: channel (\d+) is used by (.+) \((.+)\)$/,
    render: (m) => locale() === 'zh-CN'
      ? t('channel.error.conflictDetail', { channel: m[1], name: m[2], address: m[3] })
      : `channel conflicts with another visible station: channel ${m[1]} is used by ${m[2]} (${m[3]})`
  },
  {
    // ErrUnsupported wrappers. Must precede the generic capability pattern,
    // which would misparse "operation" as a capability name.
    pattern: /^operation is not supported(?:: (.+))?$/s,
    render: (m) => {
      if (locale() !== 'zh-CN') {
        return m[1] ? `operation is not supported: ${m[1]}` : 'operation is not supported';
      }
      return m[1]
        ? t('error.operationUnsupportedDetail', { detail: backendCopy(m[1]) })
        : t('error.operationUnsupported');
    }
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
    // Config setters: "<subject> must cover/must not exceed/cannot exceed/
    // must not fall below <other> of <limit> seconds, got <value>".
    pattern: /^(.+?) (must cover the|must not exceed the|cannot exceed the|must not fall below the) (.+?) of (\d+) seconds, got (\d+)$/,
    render: (m) => {
      if (locale() !== 'zh-CN') {
        return `${m[1]} ${m[2]} ${m[3]} of ${m[4]} seconds, got ${m[5]}`;
      }
      const subject = SETTINGS_SUBJECT_ZH[m[1]] ?? m[1];
      const other = SETTINGS_SUBJECT_ZH[m[3]] ?? m[3];
      const key = m[2] === 'must cover the'
        ? 'settings.error.mustCover'
        : m[2] === 'must not fall below the'
          ? 'settings.error.mustNotFallBelow'
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
