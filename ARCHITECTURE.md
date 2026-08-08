# 架构说明

`lhcontrol` 采用 Wails 桌面壳、Svelte 前端状态层、Go 应用门面、站点编排层和 BLE 传输层的分层结构。

## Go 后端

### 应用门面

- `app.go`：构造应用、Wails 绑定和配置状态。
- `api.go`：HTTP DTO、错误映射和路由注册。
- `api_server.go`：本地 HTTP 服务的监听、重试和状态维护。
- `auto_sleep.go`：自动休眠设置、事件和执行流程，以及全部用户设置项（扫描/刷新、超时、蓝牙时序、恢复、HTTP 地址）的 Wails 绑定。
- `lifecycle.go`：应用关闭顺序。

应用门面只负责协议适配和生命周期，不实现 BLE 业务规则。

### `internal/station`

该包是站点操作的业务编排边界，`Manager` 保持为调用方的统一门面：

- `types.go`：公开结果、扫描状态及 Manager 状态定义。
- `manager.go`：构造、初始化和重试状态维护。
- `coordinator.go`：全局、共享和单设备操作的并发仲裁。
- `scan.go`：扫描生命周期与扫描结果接入。
- `recovery.go`：状态读取和后台恢复调度。
- `power.go`：单站及批量电源操作。
- `channel.go`：通道安全校验和修改。
- `projection.go`：面向前端的稳定站点快照。
- `config_lifecycle.go`：重命名、配置保存和关闭。

新增业务逻辑应进入对应领域文件；不要重新创建集中承载所有流程的 Manager 文件。

### `internal/bluetooth`

该包只处理 BLE 协议、连接和设备状态，不决定舰队级业务策略。用户可调的协议时序（确认次数/间隔、重试与退避、配对写入窗口、在场漏扫阈值等）集中在 `TimingPolicy`，由应用层在加载或修改设置后通过 `ConfigureTiming` 推送，本包不依赖 config：

- `runtime.go`：适配器初始化、上下文兼容和传输错误分类。
- `model.go`：基站模型、快照和错误域。
- `scan.go`：BLE 扫描会话。
- `connection.go`：连接发现、断开和清理。
- `status.go`：状态、能力与元数据读取。
- `power.go`：电源写入及回读确认。
- `channel.go`：识别和通道读写。
- `protocol.go`：协议值解析。

## Svelte 前端

`StationStore` 是组件使用的统一门面，具体职责通过组合对象分离：

- `fleet-state.svelte.ts`：站点列表、排序、汇总和通道派生状态。
- `station-scan-controller.ts`：本地扫描、停止和周期状态刷新。
- `station-actions.ts`：电源、批量、重命名、识别、能力和通道操作。
- `external-scan.ts`：由 HTTP 等外部入口启动的扫描协调。
- `external-station-updates.ts`：外部站点快照的版本控制与合并。
- `auto-sleep-events.ts`：自动休眠事件到 UI 状态和通知的映射。
- `api-status-poller.ts`：API 与配置健康状态轮询。

组件不直接组合多个后端调用；跨请求的版本门、并发门和结果合并应留在状态/控制器层。

## 测试边界

Go 测试与上述领域文件同名或按领域命名。前端 `App.*.test.ts` 分别覆盖扫描、设备动作、状态恢复和设置；小型状态协作者保留独立单元测试。提交前运行：

```powershell
.\scripts\test.ps1
```

该入口执行 Svelte 静态检查、全部前端测试、生产构建和全部 Go 测试。
