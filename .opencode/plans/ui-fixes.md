# 前端修复计划：All 栏 On 圆角 / 无蓝牙重复错误提示 / 重复扫描按钮

已与用户确认的选择：
- On 圆角修复只改 All 栏（含顶部统计 chip），卡片内部高亮保持现状
- 卡片徽章/状态点不改，跟随后端推导
- 空状态 "Scan Now" 按钮一并删除（扫描入口只保留顶部）

## 修改 1：All 栏 On 圆角（启发式 On 不应点亮确认高亮）

根因：后端 8s booting 回退（`decodePowerStateWithHistory`，bluetooth.go:1040）把 raw
0x01/0x08 的decoded On 标记为 confirmed（IsPowerStateVerified，bluetooth.go:1867），
而 On 的确认轮询窗口约 10s（bluetooth.go:1786），批量启动返回时设备仍在启动，
前端 `allOn`（station-store.svelte.ts:193）随之点亮 On 圆角。

### frontend/src/lib/station.ts
在 `hasVerifiedPowerState` 后新增：

```ts
// Stable raw values per protocol: sleep 0x00, standby 0x02, on 0x09/0x0B.
const STABLE_POWER_RAW: Record<PowerTarget, readonly number[]> = {
  on: [0x09, 0x0b],
  standby: [0x02],
  sleep: [0x00]
};

// Stricter fleet-level confirmation. The backend's boot fallback also marks a
// decoded On backed by booting raw values (0x01/0x08) as confirmed, which
// would light the bulk bar's On thumb while stations are still spinning up;
// aggregate indicators therefore require the genuinely stable readback.
export function hasStableConfirmedPowerState(station: StationInfo, state: PowerTarget): boolean {
  return hasVerifiedPowerState(station, state) && STABLE_POWER_RAW[state].includes(station.rawPowerState);
}
```

### frontend/src/lib/state/station-store.svelte.ts
- import 列表（:20-22）加入 `hasStableConfirmedPowerState`
- fleetOn/fleetStandby/fleetSleep（:180-182）与 allOn/allStandby/allSleep（:193-195）
  全部改用 `hasStableConfirmedPowerState`
- `isCurrentPowerState` 的 import 若不再被 store 使用则移除（allOn/allStandby/allSleep
  是 store 中仅有的使用点）
- 卡片高亮（StationCard 的 isCurrentPowerState）不动

效果：启发式 On 站点计入顶部 Unconfirmed chip；设备回报 0x09/0x0B 后圆角点亮。

## 修改 2：无蓝牙设备时的重复错误提示

重复源：toast + 状态栏 + 恢复卡 + 每 15s 周期检查覆盖状态栏
（后端 ensureReady 失败，manager.go:620）。

### frontend/src/lib/state/station-store.svelte.ts
1. startScan() catch（:533-539）删除 `pushToast`，状态栏改为与恢复卡不重复的摘要：

```ts
} else {
  const classified = classifyScanError(error);
  this.scanError = classified;
  // The persistent recovery card carries the raw detail; the transient
  // toast was a third copy of the same failure, and the status line
  // keeps a short summary instead of repeating the card's detail text.
  this.statusMessage = classified.kind === 'unknown'
    ? 'Scan failed.'
    : `Scan failed: ${scanErrorCopy(classified).heading}`;
}
```
   - import 中加入 `scanErrorCopy`（同一 '../scan-error' 模块）

2. periodicStatusCheck() catch（:488）加闸门，恢复卡在场时不再覆盖状态栏：

```ts
if (this.gates.canCommitStatus(statusOperation) && !this.scanError) {
  this.statusMessage = `Status refresh incomplete: ${error}`;
}
```

## 修改 3：删除重复的扫描按钮

ScanRecovery 的 "Retry scan" 与空状态 "Scan Now" 都只调用 store.startScan()，
与顶部 AppHeader Scan 完全重复。

### frontend/src/lib/components/ScanRecovery.svelte
- 删除 `<button class="btn primary" ...>Retry scan</button>`
- props 只保留 `kind`、`detail`；移除 `retryDisabled`、`onRetry`
- 移除 `RefreshCw` 引入；删除 `.recovery .btn` 样式与对应媒体查询行
- 保留 role="alert" 与现有文案结构

### frontend/src/lib/components/FleetView.svelte
- ScanRecovery 使用处移除 `retryDisabled={scanLocked}` 与 `onRetry={onScan}`
- 空状态 `{:else if !scanError}` 块中删除 Scan Now 按钮，仅保留图标与文案
- 删除不再使用的 props：`onScan`、`scanLocked`（含类型声明）

### frontend/src/App.svelte
- FleetView 使用处移除 `scanLocked={store.scanLocked}` 与 `onScan={...}`（:172, :181）
- AppHeader 保持不变（唯一扫描入口）

## 修改 4：DetailsDrawer raw 十六进制显示

### frontend/src/lib/components/DetailsDrawer.svelte（:93）
`(raw {station.rawPowerState})` → 未知(-1)显示 `(raw —)`，否则
`(raw 0x09)` 风格：

```svelte
(script 内)
const rawPowerLabel = $derived(station.rawPowerState < 0
  ? '—'
  : `0x${station.rawPowerState.toString(16).toUpperCase().padStart(2, '0')}`);
```

## 测试更新

### frontend/src/lib/station.test.ts
- 新增 hasStableConfirmedPowerState 用例：
  - confirmed + raw 0x09/0x0B → true
  - confirmed + raw 0x01/0x08（booting 回退）→ false
  - standby/sleep 精确 raw → true；powerFresh=false → false

### frontend/src/lib/state/station-store.test.ts
- 'classifies a failed scan...'（:93-101）：
  - 断言 `pushToast` 未以 'Scan failed' 调用
  - statusMessage 断言改为 `'Scan failed: Bluetooth is unavailable'`
- 新增 allOn 严格性用例：stations 全部 {powerState:1, confirmed:true, raw:0x01}
  时 allOn=false、fleetOn=0；raw 0x0B 时 allOn=true

### frontend/src/lib/components/ScanRecovery.test.ts
- 移除 Retry scan 相关测试；改为断言渲染 heading/steps/detail 且
  `queryByRole('button')` 不存在重试按钮；移除 retryDisabled 用例

### frontend/src/lib/components/FleetView.test.ts
- 'shows the idle empty state and retries...'（:78-86）：改为断言
  'No base stations found.' 存在且无任何扫描按钮
- 'locks the idle scan button...'（:88-91）：删除
- 'keeps the recovery card...'（:105-115）：移除点击 Retry scan 部分，
  断言恢复卡存在、无 'Retry scan' 按钮
- defaultProps 移除 onScan/scanLocked 字段（:36, :44）

### frontend/src/App.test.ts
- 检查是否向 FleetView 传 onScan/scanLocked，如有则同步移除（实施时确认）

## 验证

```powershell
npm run test   # frontend 目录，vitest run
npm run check  # frontend 目录，svelte-check
```

无 Go 改动，不需跑 go test。

## 不做的事（已确认）

- 卡片 seg 高亮/徽章/状态点不收紧（跟随后端）
- toast 按相似度去重（低优先级，本次不动）
- 后端 IsPowerStateVerified 不改（其行为有测试守护，前端严格化即可）
