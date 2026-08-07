import { GetLanguage } from './backend';

export type Locale = 'en' | 'zh-CN';
export type LanguagePreference = 'system' | Locale;
export type MessageValues = Record<string, string | number>;

const zhCN = {
  'Automatic refresh': '自动刷新',
  'Scanning and refresh': '扫描与刷新',
  'Scan when the application starts': '应用启动时扫描',
  'Discover nearby stations automatically after startup.': '应用启动后自动发现附近的基站。',
  'Bluetooth scan duration': '蓝牙扫描时长',
  'Longer scans can find slow advertisers but take more time. Allowed range: 2–30 seconds.': '较长的扫描更容易发现广播缓慢的设备，但耗时也更长。允许范围：2–30 秒。',
  'Refresh station status automatically': '自动刷新基站状态',
  'API health continues to be monitored when station refresh is disabled.': '关闭基站刷新后仍会继续监测 API 运行状态。',
  'Displayed state remains valid long enough for the selected interval. Allowed range: 5–300 seconds.': '显示状态的有效期会自动适配所选轮询间隔。允许范围：5–300 秒。',
  'Loading scan and refresh settings...': '正在加载扫描与刷新设置…',
  'Startup scan setting could not be loaded': '无法加载启动扫描设置',
  'Startup scan setting could not be saved': '无法保存启动扫描设置',
  'Scan duration could not be loaded': '无法加载扫描时长',
  'Scan duration could not be saved': '无法保存扫描时长',
  'Automatic station refresh setting could not be loaded': '无法加载自动基站刷新设置',
  'Automatic station refresh setting could not be saved': '无法保存自动基站刷新设置',
  'Status polling interval': '状态轮询间隔',
  'Controls how often station states and application health are refreshed automatically. Allowed range: 5–300 seconds.': '控制自动刷新基站状态和应用运行状况的频率。允许范围：5–300 秒。',
  'Loading automatic refresh settings...': '正在加载自动刷新设置…',
  'Status polling interval could not be loaded': '无法加载状态轮询间隔',
  'Status polling interval could not be saved': '无法保存状态轮询间隔',
  'Operation safety': '操作安全',
  'Bluetooth diagnostics': '蓝牙诊断',
  'Bulk power timeout': '批量电源操作超时',
  'seconds': '秒',
  'A bulk power action is stopped when this total time limit is reached. Allowed range: 30–600 seconds.': '批量电源操作达到此总时限后会自动停止。允许范围：30–600 秒。',
  'Loading operation safety settings...': '正在加载操作安全设置…',
  'Bulk power timeout could not be loaded': '无法加载批量电源操作超时设置',
  'Bulk power timeout could not be saved': '无法保存批量电源操作超时设置',
  'Cancel bulk power': '停止批量电源操作',
  'Stopping bulk power...': '正在停止批量电源操作…',
  'Bulk {target} cancelled': '批量{target}操作已停止',
  'Bulk {target} timed out': '批量{target}操作已超时',
  'Auto sleep timed out: {success} confirmed, {unconfirmed} unconfirmed, {failed} failed, {timedOutSkipped} skipped due to timeout.': '自动休眠超时：已确认 {success} 个、未确认 {unconfirmed} 个、失败 {failed} 个、因超时跳过 {timedOutSkipped} 个。',
  'SteamVR base stations': 'SteamVR 基站',
  'Fleet power summary': '全部基站电源状态摘要',
  'On': '开启',
  'Standby': '待机',
  'Sleep': '休眠',
  'Booting': '启动中',
  'Unknown': '未知',
  'Unconfirmed': '未确认',
  'State not confirmed by a fresh read': '状态尚未通过最新读取确认',
  'Unconfirmed stations have a power state that was not confirmed by a fresh read.': '未确认基站的电源状态尚未通过最新读取确认。',
  'Stopping...': '正在停止…',
  'Stop': '停止',
  'Scan': '扫描',
  'All': '全部',
  'Set all known stations': '设置所有已知基站',
  'No actionable station': '没有可操作的基站',
  'Bluetooth operation in progress': '蓝牙操作正在进行',
  'Turn all known stations on': '开启所有已知基站',
  'Set all known stations to standby': '将所有已知基站设为待机',
  'Put all known stations to sleep': '让所有已知基站进入休眠',
  'Settings': '设置',
  'Open settings': '打开设置',
  '{known} known stations · {untrusted} not fully verified — bulk commands include them.': '{known} 个已知基站 · {untrusted} 个尚未完全验证 — 批量命令仍会包含它们。',
  'Channel conflict: {detail}': '频道冲突：{detail}',
  'Scanning for base stations...': '正在扫描基站…',
  'External scan in progress...': '外部扫描正在进行…',
  'Reading station states...': '正在读取基站状态…',
  'Discovering nearby stations...': '正在发现附近基站…',
  'No base stations found.': '未发现基站。',
  'Station details': '基站详情',
  'Close': '关闭',
  'Close station details': '关闭基站详情',
  'Status': '状态',
  'Power': '电源',
  'Channel': '频道',
  'Connection': '连接',
  'Last seen': '最后发现',
  'Last status read': '最后状态读取',
  'Power data': '电源数据',
  'Channel data': '频道数据',
  'confirmed': '已确认',
  'unverified': '未验证',
  'last known, stale': '上次已知，已过期',
  'fresh': '最新',
  'stale or unavailable': '已过期或不可用',
  'never': '从未',
  'Unable to verify': '无法验证',
  'Capabilities could not be verified. Power commands will retry discovery; unsupported operations will be reported.': '无法验证功能支持。电源命令会重新尝试发现功能；不支持的操作将被报告。',
  'Refresh capabilities': '刷新功能信息',
  'Send the identify signal': '发送识别信号',
  'Recheck support and identify': '重新检查支持情况并识别',
  'Identify': '识别',
  'Recheck support and change Channel safely': '重新检查支持情况并安全更改频道',
  'Change Channel': '更改频道',
  'Identify may wake a sleeping base station.': '识别操作可能会唤醒休眠中的基站。',
  'Identity': '身份信息',
  'Original name': '原始名称',
  'Address': '地址',
  'Metadata': '元数据',
  'Device information': '设备信息',
  'Manufacturer': '制造商',
  'Model': '型号',
  'Serial number': '序列号',
  'Hardware': '硬件',
  'Firmware': '固件',
  'Capabilities': '功能支持',
  'Other': '其他',
  'read': '读取',
  'write': '写入',
  'notify': '通知',
  'standby': '待机',
  'identify': '识别',
  'device info': '设备信息',
  'loaded and fresh': '已加载且为最新',
  'cached, stale': '已缓存，已过期',
  'unavailable': '不可用',
  'Preferences': '偏好设置',
  'Close settings': '关闭设置',
  'Language': '语言',
  'Display language': '显示语言',
  'Follow system': '跟随系统',
  'Changes apply immediately and are saved for the next start.': '更改会立即生效，并保存供下次启动使用。',
  'Bluetooth adapter': '蓝牙适配器',
  'Detecting Bluetooth adapters...': '正在检测蓝牙适配器…',
  'Retry': '重试',
  'Detected Bluetooth adapters': '检测到的蓝牙适配器',
  'No Bluetooth adapters were detected on this system.': '此系统未检测到蓝牙适配器。',
  'Refresh adapters': '刷新适配器',
  'Windows controls which radio handles BLE discovery and connections. The application cannot route a Lighthouse operation through one specific adapter.': 'Windows 决定由哪个无线电设备处理 BLE 发现和连接。应用无法指定某个适配器执行 Lighthouse 操作。',
  'Auto sleep': '自动休眠',
  'Sleep all stations after a session ends': '会话结束后休眠所有基站',
  'Scans and puts every known station to sleep': '扫描并让每个已知基站进入休眠',
  'Enable auto sleep': '启用自动休眠',
  'Auto sleep trigger': '自动休眠触发方式',
  'fires while the Steam client stays open': 'Steam 客户端保持打开时也会触发',
  'fires only when the Steam client fully exits': '仅在 Steam 客户端完全退出时触发',
  'Wait before sleeping': '休眠前等待',
  'minutes': '分钟',
  'The timer starts when the watched process closes. Reopening it cancels pending or in-progress automatic sleep. Commands already completed are kept and reported; commands not yet sent are skipped. When the timer fires, a Bluetooth operation from you skips this round instead of retrying. Settings are saved and restored on the next start.': '被监视的进程关闭后开始计时。重新打开该进程会取消等待中或进行中的自动休眠。已完成的命令会被保留并报告，尚未发送的命令会被跳过。计时结束时若你正在执行蓝牙操作，本轮自动休眠会被跳过而不会重试。设置会保存并在下次启动时恢复。',
  'Loading auto sleep settings...': '正在加载自动休眠设置…',
  'Language setting could not be saved': '语言设置无法保存',
  'Auto-sleep settings could not be loaded': '自动休眠设置无法加载',
  'Auto-sleep settings could not be saved': '自动休眠设置无法保存',
  'Close notification': '关闭通知',
  'Error': '错误',
  'Warning': '警告',
  'Information': '信息',
  'Success': '成功',
  'API online': 'API 在线',
  'API offline': 'API 离线',
  'Configuration changes cannot be saved': '配置更改无法保存',
  'Ready to scan.': '可以开始扫描。',
  'just now': '刚刚',
  'clock mismatch': '系统时间不一致',
  '{count}s ago': '{count} 秒前',
  '{count}m ago': '{count} 分钟前',
  '{count}h ago': '{count} 小时前',
  '{count}d ago': '{count} 天前',
  'Run a new scan before changing the channel.': '更改频道前请重新扫描。',
  'Wait for the station to finish booting before changing its channel.': '请等待基站完成启动后再更改频道。',
  'Save an empty name to restore the original name': '保存空名称可恢复原始名称',
  'Station name': '基站名称',
  'Save name': '保存名称',
  'Cancel': '取消',
  'Cancel rename': '取消重命名',
  'Saving an empty name restores the original name: {name}.': '保存空名称将恢复原始名称：{name}。',
  'Rename': '重命名',
  'Rename {name}': '重命名 {name}',
  'Last known channel': '上次已知频道',
  'Last known state; last successful read {time}': '上次已知状态；最后成功读取于{time}',
  'unknown': '未知',
  'State reported by the station but not confirmed by readback': '基站已报告状态，但尚未通过回读确认',
  'Not detected in the latest scan; direct power control can still be attempted': '最近一次扫描未检测到；仍可尝试直接控制电源',
  'not visible': '不可见',
  'not detected in the latest scan, but direct power control can still be attempted': '最近一次扫描未检测到，但仍可尝试直接控制电源',
  'Its connection could not be fully released before the last scan, so the advertisement may have been missed': '上次扫描前连接未能完全释放，因此可能错过了广播',
  'presence uncertain': '存在状态不确定',
  'the connection could not be fully released before the last scan, so the advertisement may have been missed': '上次扫描前连接未能完全释放，因此可能错过了广播',
  'Missed by one scan; retained until a second consecutive miss': '一次扫描未发现；连续第二次未发现前仍会保留',
  'scan stale': '扫描状态过期',
  'missed by one scan, retained until a second consecutive miss': '一次扫描未发现；连续第二次未发现前仍会保留',
  'Power control for {name}': '{name} 的电源控制',
  'Turn {name} on': '开启 {name}',
  'Turn lasers and motor on': '开启激光器和电机',
  'Set {name} to standby': '将 {name} 设为待机',
  'Lasers off, motor remains powered': '关闭激光器，电机保持供电',
  'Put {name} to sleep': '让 {name} 进入休眠',
  'Turn lasers and motor off': '关闭激光器和电机',
  'Details': '详情',
  'Details for {name}': '{name} 的详情',
  'Notifications': '通知',
  'Dismiss': '忽略',
  'Dismiss notification': '关闭通知',
  'HTTP API unavailable': 'HTTP API 不可用',
  'Config warning': '配置警告',
  'Config read-only': '配置只读',
  'API ready': 'API 就绪',
  'Confirm bulk power': '确认批量电源操作',
  'Bulk power': '批量电源',
  'Set all known stations to {target}': '将所有已知基站设为{target}',
  'Close bulk power confirmation': '关闭批量电源确认',
  'Some stations are not fully verified. Commands are sent to every station the backend still considers reachable; results may come back unconfirmed.': '部分基站尚未完全验证。命令会发送给后端仍认为可连接的每个基站；结果可能无法确认。',
  'Visible & verified': '可见且已验证',
  'Presence uncertain': '存在状态不确定',
  'Not seen in latest scan': '最近扫描未发现',
  'Actionable for {target}': '可执行{target}',
  'Applying bulk power...': '正在应用批量电源操作…',
  'Turn on': '开启',
  'Set to standby': '设为待机',
  'Put to sleep': '进入休眠',
  '{verb} {count} station': '{verb} {count} 个基站',
  '{verb} {count} stations': '{verb} {count} 个基站',
  'Optical channels': '光学频道',
  'Channel occupancy': '频道占用情况',
  'CH {channel} — free': 'CH {channel} — 空闲',
  '{name} · {state} · last-known': '{name} · {state} · 上次已知',
  'Change channel': '更改频道',
  'Safe channel change': '安全更改频道',
  'Close channel editor': '关闭频道编辑器',
  'Current channel': '当前频道',
  'Target channel': '目标频道',
  'Channel {channel}, occupied by {names}': '频道 {channel}，已被 {names} 占用',
  'Occupied by {names}': '已被 {names} 占用',
  'Channel {channel}': '频道 {channel}',
  'Struck-through channels are occupied by a visible station. The dot marks the current channel.': '带删除线的频道已被可见基站占用，圆点表示当前频道。',
  'I understand that a visible station has an unknown channel, so a conflict cannot be fully ruled out.': '我了解某个可见基站的频道未知，因此无法完全排除冲突。',
  'Writing channel and verifying the readback...': '正在写入频道并验证回读…',
  'The value is only accepted after the base station reads back the requested channel. Failure will not trigger an automatic rollback.': '仅在基站回读到请求的频道后才会接受该值。失败不会触发自动回滚。',
  'Identify this station': '识别此基站',
  'Confirm change': '确认更改',
  'Configuration changes cannot be saved.': '配置更改无法保存。',
  'Bluetooth is unavailable': '蓝牙不可用',
  'The scan could not run because the Bluetooth radio is off or unavailable.': '由于蓝牙无线电已关闭或不可用，无法执行扫描。',
  'Open Windows Settings → Bluetooth & devices and turn Bluetooth on.': '打开 Windows 设置 → 蓝牙和设备，然后开启蓝牙。',
  'Wait a moment for the adapter to become ready, then retry the scan.': '等待适配器准备就绪，然后重试扫描。',
  'No Bluetooth adapter found': '未找到蓝牙适配器',
  'The scan could not run because no Bluetooth adapter is present.': '由于系统中没有蓝牙适配器，无法执行扫描。',
  'Plug in or re-enable the Bluetooth adapter.': '插入或重新启用蓝牙适配器。',
  'Check Device Manager for a disabled or missing adapter, then retry the scan.': '在设备管理器中检查禁用或缺失的适配器，然后重试扫描。',
  'Bluetooth access was denied': '蓝牙访问被拒绝',
  'The scan could not run because this app is not allowed to use Bluetooth.': '由于应用没有蓝牙使用权限，无法执行扫描。',
  'Grant Bluetooth permission to the app (or run it with the required rights).': '授予应用蓝牙权限（或使用所需权限运行）。',
  'Retry the scan once access is allowed.': '允许访问后重试扫描。',
  'The scan timed out': '扫描超时',
  'The scan did not finish in time; the adapter may be busy or a station may be out of range.': '扫描未能及时完成；适配器可能正忙，或基站超出范围。',
  'Move closer to the base stations and keep the adapter unobstructed.': '靠近基站，并确保适配器周围无遮挡。',
  'Retry the scan; repeated timeouts may indicate an adapter problem.': '重试扫描；反复超时可能表示适配器存在问题。',
  'Scan failed': '扫描失败',
  'The scan could not be completed.': '扫描无法完成。',
  'Check that the Bluetooth adapter is connected and the base stations are powered.': '检查蓝牙适配器是否已连接，以及基站是否已通电。',
  'Retry the scan; the error details below are preserved for diagnostics.': '重试扫描；下方保留了错误详情以便诊断。',
  'Preparing external scan...': '正在准备外部扫描…',
  'Status refresh incomplete': '状态刷新未完成',
  'Scan stopped.': '扫描已停止。',
  'Scan failed.': '扫描失败。',
  'Scan failed: {heading}': '扫描失败：{heading}',
  'Stopping scan...': '正在停止扫描…',
  'Unable to stop scan': '无法停止扫描',
  'Switching to {target}…': '正在切换到{target}…',
  'Setting {name} to {target}…': '正在将 {name} 设为{target}…',
  'Already {target}': '已经是{target}',
  '{target} confirmed': '已确认{target}',
  '{target} sent · confirmation failed': '已发送{target} · 确认失败',
  '{target} sent · status unavailable': '已发送{target} · 状态不可用',
  '{target} sent · {detail}': '已发送{target} · {detail}',
  '{name} is already {target}; no command was sent.': '{name} 已经是{target}；未发送命令。',
  '{name} is {target}.': '{name} 当前为{target}。',
  '{name}: command sent, but confirmation failed. {detail}': '{name}：命令已发送，但确认失败。{detail}',
  '{name}: {target} command sent; this firmware cannot confirm the state.': '{name}：已发送{target}命令；此固件无法确认状态。',
  'Failed · {readback}': '失败 · {readback}',
  'Power change failed for {name}': '{name} 的电源更改失败',
  'Setting all available stations to {target}…': '正在将所有可用基站设为{target}…',
  'Bulk {target} operation partially failed': '批量{target}操作部分失败',
  'Rename blocked: another operation is in progress for {name}.': '无法重命名：{name} 正在进行另一项操作。',
  'Renamed to {name}.': '已重命名为 {name}。',
  'Reset name for {name}.': '已恢复 {name} 的名称。',
  'Error renaming': '重命名出错',
  'Identify signal sent to {name}.': '已向 {name} 发送识别信号。',
  'Identify failed for {name}': '{name} 识别失败',
  'Capabilities refreshed for {name}, but some values are unavailable': '已刷新 {name} 的功能信息，但部分值不可用',
  'Capabilities refreshed for {name}.': '已刷新 {name} 的功能信息。',
  'Capability refresh failed for {name}': '{name} 的功能信息刷新失败',
  '{name}: channel command sent, but confirmation failed. {detail}': '{name}：频道命令已发送，但确认失败。{detail}',
  'Channel changed from {previous} to {channel}. {warnings}': '频道已从 {previous} 更改为 {channel}。{warnings}',
  'Channel already set to {channel}; no command was sent. {warnings}': '频道已经是 {channel}；未发送命令。{warnings}',
  'Channel change failed': '频道更改失败',
  'Auto sleep: scanning and putting all stations to sleep...': '自动休眠：正在扫描并休眠所有基站…',
  'Session ended — scanning and putting all stations to sleep.': '会话已结束 — 正在扫描并休眠所有基站。',
  'Auto sleep finished: {success} station(s) put to sleep.': '自动休眠完成：{success} 个基站已进入休眠。',
  'Auto sleep finished: {success} confirmed, {unconfirmed} unconfirmed, {failed} failed, {skipped} skipped.': '自动休眠完成：{success} 个已确认，{unconfirmed} 个未确认，{failed} 个失败，{skipped} 个跳过。',
  'Auto sleep cancelled: {details}.': '自动休眠已取消：{details}。',
  'Auto sleep skipped': '已跳过自动休眠',
  'Auto sleep failed': '自动休眠失败',
  'Bluetooth busy': '蓝牙正忙',
  'unknown error': '未知错误',
  '{success} succeeded, {failed} failed, {skipped} skipped': '{success} 个成功，{failed} 个失败，{skipped} 个跳过',
  '{success} confirmed, {unconfirmed} unconfirmed, {failed} failed, {skipped} skipped': '{success} 个已确认，{unconfirmed} 个未确认，{failed} 个失败，{skipped} 个跳过',
  'no station commands completed': '没有完成任何基站命令',
  'External scan': '外部扫描',
  '{prefix} stopped.': '{prefix}已停止。',
  '{prefix} failed': '{prefix}失败',
  '{count} known station.': '{count} 个已知基站。',
  '{count} known stations.': '{count} 个已知基站。',
  'found {found}; {knownLabel}': '发现 {found} 个；{knownLabel}',
  'no stations found in this scan.': '本次扫描未发现基站。',
  '{prefix} completed: {summary}': '{prefix}完成：{summary}',
  '{confirmed} confirmed; {unconfirmed} sent but unconfirmed; {already} already at target; {skipped} skipped': '{confirmed} 个已确认；{unconfirmed} 个已发送但未确认；{already} 个已是目标状态；{skipped} 个已跳过',
  '{counts}; {failed} failed for {target}': '{counts}；{failed} 个{target}操作失败',
  '{counts} for {target}.': '{target}：{counts}。',
  'command failed': '命令失败',
  'Auto sleep finished: {success} succeeded, {failed} failed.': '自动休眠完成：{success} 个成功，{failed} 个失败。',
  'Auto sleep finished: {success} succeeded, {failed} failed, {skipped} skipped.': '自动休眠完成：{success} 个成功，{failed} 个失败，{skipped} 个跳过。',
  'Auto sleep finished: {success} succeeded, {skipped} skipped.': '自动休眠完成：{success} 个成功，{skipped} 个跳过。',
  'actual': '实际',
  'last-known': '上次已知',
  'Skipped · {reason}': '已跳过 · {reason}',
  'not actionable': '不可操作',
  'status unavailable': '状态不可用',
  'Failed to set {target}': '无法设为{target}',
  'Channel command sent but unconfirmed': '频道命令已发送但未确认',
  'Readback': '回读',
  'connected': '已连接',
  'disconnected': '已断开'
} as const;

export type TranslationKey = keyof typeof zhCN;

let currentLocale = $state<Locale>('en');
let currentLanguagePreference = $state<LanguagePreference>('system');

export function locale(): Locale {
  return currentLocale;
}

export function languagePreference(): LanguagePreference {
  return currentLanguagePreference;
}

export function isLocale(value: string): value is Locale {
  return value === 'en' || value === 'zh-CN';
}

export function localeFromLanguages(languages: readonly string[] = []): Locale {
  for (const language of languages) {
    const normalized = language.toLowerCase();
    if (normalized.startsWith('zh')) return 'zh-CN';
    if (normalized.startsWith('en')) return 'en';
  }
  return 'en';
}

export function systemLocale(): Locale {
  if (typeof navigator === 'undefined') return 'en';
  const languages = navigator.languages?.length ? navigator.languages : [navigator.language];
  return localeFromLanguages(languages);
}

export function setLocale(next: Locale): void {
  currentLocale = next;
  if (typeof document !== 'undefined') document.documentElement.lang = next;
}

export function setLanguagePreference(next: LanguagePreference): Locale {
  currentLanguagePreference = next;
  const effective = next === 'system' ? systemLocale() : next;
  setLocale(effective);
  return effective;
}

export async function initializeLocale(): Promise<Locale> {
  let preference: LanguagePreference = 'system';
  try {
    const persisted = await GetLanguage();
    if (isLocale(persisted)) preference = persisted;
  } catch {
    // A missing WebView binding or unreadable config must not block startup.
  }
  return setLanguagePreference(preference);
}

function interpolate(template: string, values: MessageValues): string {
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    Object.prototype.hasOwnProperty.call(values, name) ? String(values[name]) : match);
}

export function t(key: TranslationKey, values: MessageValues = {}): string {
  const template = currentLocale === 'zh-CN' ? zhCN[key] : key;
  return interpolate(template, values);
}

export function withDetail(key: TranslationKey, detail: unknown): string {
  const raw = String(detail);
  return raw ? `${t(key)}: ${raw}` : t(key);
}
