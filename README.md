一个通过 **Bluetooth Low Energy（BLE，低功耗蓝牙）** 控制 Valve Lighthouse（SteamVR）2.0 定位基站电源状态的简单应用程序。

![pasted-image.png · 83](https://img.cdn1.vip/i/6a67245d654e5_1785144413.webp)

Modified based on [lhcontrol](https://github.com/FlameInTheDark/lhcontrol). Special thanks to the original author for making the project open source.

基于 https://github.com/FlameInTheDark/lhcontrol 修改 感谢原作者开源

`lhcontrol` 目前仅支持 Windows。项目中捆绑的蓝牙依赖包含针对 Windows WinRT 的稳定性补丁，因此不适用于 Linux 或 macOS 编译。

## 功能特性

* 扫描附近的 Lighthouse 2.0 定位基站。
* 显示 Lighthouse 2.0 基站的完整电源状态：

  * Sleep（休眠）
  * Standby（待机）
  * Booting（启动中）
  * On（运行中）
  * Unknown（未知）
* 显示 Lighthouse 2.0 基站上报的光学通道号（1-16）。
* 单独控制指定基站进入：

  * On
  * Standby
  * Sleep
* 在设备支持的情况下，通过闪烁 LED 来识别指定的实体基站。
* 检测基站之间的通道冲突，并通过强制回读验证的方式安全修改通道。
* 显示基站相关详细信息，包括：

  * 固件版本
  * 硬件版本
  * 型号
  * 序列号
  * 制造商
  * BLE 功能支持情况
* 一键将当前应用会话中已经发现的全部基站设为 On、Standby 或 Sleep。
* 支持为基站设置本地名称，方便区分和管理。
* 界面支持 English 和简体中文；未保存语言偏好时会跟随系统语言，也可在设置中即时切换并保存选择。
* 在单次应用运行期间持续保存已经发现的基站，即使后续扫描未再次发现，也不会立即从列表中移除。
* 支持在设置中查看 Windows 检测到的蓝牙适配器，用于诊断无线电可用性；Windows 系统负责选择实际执行 BLE 扫描和连接的无线电。
* 支持自动休眠：监控 SteamVR（`vrserver.exe`）或 Steam（`steam.exe`），在对应会话结束并经过 1-120 分钟延迟后，重新扫描并将全部已知基站设为 Sleep。
* 自动处理外部 HTTP 扫描触发、扫描取消和恢复状态，并在界面中显示 API、配置持久化及设备可见性提示。
* Windows 单实例运行；再次启动时会尝试将现有窗口带到前台。

## 技术栈

* **应用框架：** [Wails v2](https://wails.io/)
* **后端：** Go
* **前端：** Svelte
* **蓝牙：** [TinyGo Bluetooth Library](https://github.com/tinygo-org/bluetooth)

## 环境要求

* **操作系统：** Windows 10 或 Windows 11，x64。
* **蓝牙适配器：** 支持 Bluetooth Low Energy 的 Windows 蓝牙适配器，并建议安装最新驱动。
* **Go：** 1.25.12。
* **Node.js：** 22.14 或更高版本，建议使用 Node.js 22 系列。
* **Wails CLI：**

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
```

* **NSIS：** 仅在构建 Windows 安装程序时需要，要求版本 3.12：

```bash
wails build -nsis
```

## 项目初始化

### 1. 克隆仓库

```bash
git clone https://github.com/FlameInTheDark/lhcontrol
cd lhcontrol
```

### 2. 安装前端依赖

通常 Wails 会在构建时自动处理前端依赖，也可以手动执行：

```bash
cd frontend
npm ci
cd ..
```

## 测试

Go 根包内嵌了生成的前端资源，因此全新检出必须先构建 `frontend/dist` 才能运行 `go test ./...`。请使用仓库的测试入口安装依赖、检查并测试前端、构建内嵌资源并运行全部 Go 测试：

```powershell
.\scripts\test.ps1
```

Bash 环境：

```bash
./scripts/test.sh
```

## 运行应用

### 开发模式

支持实时重载：

```bash
wails dev
```

### 生产构建

```bash
wails build
```

构建完成后，Windows 可执行文件会生成在：

```text
build/bin
```

项目的 Releases 中也可能提供预构建安装程序：

```text
lhcontrol-amd64-installer.exe
```

## 使用方法

1. 启动应用程序。
2. 点击 **Scan**，扫描附近的 Lighthouse 基站。
3. 应用会尝试连接发现的基站，并读取当前电源状态。
4. 通过 **Power** 菜单将指定基站设置为：

   * On
   * Standby
   * Sleep
5. 打开 **Details** 可以查看设备能力和详细信息，同时可以：

   * 识别实体基站
   * 安全修改光学通道
6. 使用 **All** 控件可以控制当前应用会话中发现过的全部基站，包括最近一次扫描没有再次发现的历史设备。
7. 打开右上角的 **Settings**：

   * 在 **Language** 中切换 English 或简体中文。更改会立即生效，并在下次启动时继续使用；首次启动或尚未保存选择时，应用会根据 Windows 系统语言自动选择界面语言。
   * 在 **Scanning and refresh** 中决定是否启动时自动扫描、设置 2-30 秒扫描时长、启用或关闭自动基站状态刷新，并设置 5-300 秒轮询间隔。较长轮询间隔会自动延长界面状态有效期，但电源写入仍使用独立的 45 秒安全有效期，过期时会先强制读取状态。
   * 启用 **Auto sleep**，选择监控 SteamVR 或 Steam，并设置 1-120 分钟延迟。
   * 在 **Operation safety** 中设置批量电源操作总超时（30–600 秒）与单站操作超时（30–120 秒，批量超时必须不小于它）。
   * **Power operation timing**、**Connection timing**、**Channel and presence**、**Read budgets**、**Background recovery** 提供高级蓝牙时序设置（确认次数与间隔、重试与退避、读取预算、恢复调度等），默认值适合常见环境，通常无需修改。
   * 在 **HTTP API** 中修改本地 HTTP 接口监听地址（保存后立即重启到新地址），并在 **Bluetooth diagnostics** 中查看 Windows 检测到的蓝牙适配器。

自动休眠只会在监控进程至少运行过一次、随后退出并持续超过设定延迟时触发；如果进程在延迟期间或自动休眠执行期间重新启动，本轮操作会取消，尚未发送的命令不会继续，界面会报告取消前已经完成、失败及跳过的基站数量。批量超时时同样会保留部分执行结果，并分别报告已确认、未确认、失败和因超时跳过的数量。触发后应用先扫描基站，再将全部已知基站设为 Sleep。如果此时已有其他蓝牙操作，本轮自动休眠会跳过，不会自动重试。

## 设置与本地数据

基站别名、扫描与刷新设置、批量操作与单站操作超时、电源/连接/通道等高级时序设置、后台恢复设置、HTTP API 监听地址、自动休眠设置和显式选择的界面语言会保存到：

```text
%APPDATA%\lhcontrol\config.json
```

如果配置文件内容无效，应用会保留带时间戳的 `config.json.invalid-*` 副本并恢复默认配置；界面底部会显示配置只读或保存失败提示。

## 故障排查

### 扫描异常

如果第一次扫描正常，但之后扫描失败，或者操作设备时出现类似以下错误：

```text
characteristic not found
```

可以尝试：

1. 在 Windows 蓝牙设备列表中删除对应 Lighthouse 基站。
2. 重启计算机。
3. 不要在 Windows 设置中重新主动配对基站。
4. 启动 `lhcontrol`，让程序通过 BLE 扫描自行发现基站。

### Bluetooth unavailable

如果蓝牙被关闭，或者蓝牙适配器临时断开：

1. 重新开启蓝牙或重新连接蓝牙适配器。
2. 等待大约两秒。
3. 再次执行扫描。

运行中的应用会尝试重新初始化蓝牙适配器，通常不需要重新启动程序。

### 蓝牙驱动

请确保使用最新版本的蓝牙适配器驱动程序。

### 权限

部分 Windows 环境可能需要允许应用访问蓝牙硬件及相关系统权限。

## Windows 硬件验证

保持 `lhcontrol` 应用运行，然后执行：

```powershell
.\scripts\hardware-smoke.ps1
```

该脚本默认执行 10 次连续扫描，用于验证：

* 多次扫描过程是否稳定。
* Lighthouse 基站排序是否稳定。
* 设备发现情况是否符合预期。
* 每次扫描的执行时间。
* 扫描失败时的原始状态和错误。
* 电源请求失败时的请求信息、验证信息及错误。

验证报告会保存到：

```text
build\verification
```

可以使用以下参数定义硬件验收范围：

```powershell
-MinimumStations
-ExpectedAddresses
-ScanCycles
```

设备断言只针对**每次扫描中实际发现的设备**，不会使用应用会话中的历史设备记录进行判断。

### 电源状态验证

仅在确认可以安全操作所有已知 Lighthouse 基站时使用：

```powershell
.\scripts\hardware-smoke.ps1 -ExercisePower
```

该模式会测试：

* On
* Standby
* Sleep

脚本要求所有基站的初始状态均能够被确认。

每次电源操作都会执行状态回读验证，并在 `finally` 阶段尝试恢复所有设备的初始状态。

如果状态恢复失败，脚本会以失败状态退出。

### 无硬件自检

可以执行：

```powershell
.\scripts\hardware-smoke.ps1 -SelfTest
```

该模式无需蓝牙硬件，用于验证报告生成及断言逻辑本身。

## 诊断日志

日志文件位于：

```text
%APPDATA%\lhcontrol\lhcontrol.log
```

单个日志文件最大限制为：

```text
5 MB
```

同时保留一个上一段日志：

```text
lhcontrol.log.1
```

---

# HTTP API

为了方便外部脚本、自动化程序或其他应用集成，`lhcontrol` 会在本机提供一个简单的 HTTP API：

```text
http://127.0.0.1:7575
```

API 默认监听本机回环地址（`127.0.0.1:7575`），可在设置中修改监听地址（保存后立即重启到新地址）。JSON 请求体上限为 16 KiB。常见错误会以 `{"error":"..."}` 返回；当已有前台蓝牙操作时通常返回 `409 Conflict`。

## `GET /health`

返回本地 API 和配置持久化状态：

```json
{
  "running": true,
  "address": "127.0.0.1:7575",
  "error": "",
  "warnings": [],
  "configWritable": true,
  "activeOperations": [],
  "operationRevision": 42
}
```

该接口适合启动探测及诊断；`running` 表示 API 监听器当前是否正常运行，`warnings` 会包含配置加载或保存警告。`activeOperations` 列出当前正在执行的外部可见操作（每项包含 `id` 和 `kind`，例如批量电源、单站电源或自动休眠；扫描走独立的扫描事件流，不在该列表中）；`operationRevision` 是单调递增的操作序号，界面用它对外部站点快照做版本门控，客户端合并状态时应比较该值而不是假设某个原子时刻。

---

## `POST /allon`

尝试开启当前应用会话中所有已知 Lighthouse 基站。

**请求体：**

无。

**响应：**

返回：

```text
200 OK
```

并附带每个基站的详细执行结果。

如果当前已经存在其他前台蓝牙操作：

```text
409 Conflict
```

---

## `POST /alloff`

尝试将当前应用会话中所有已知 Lighthouse 基站设置为 Sleep。

**请求体：**

无。

**响应：**

返回：

```text
200 OK
```

并附带每个基站的详细执行结果。

如果当前已经存在其他前台蓝牙操作：

```text
409 Conflict
```

---

## `GET /status`

返回当前应用会话中已知的 Lighthouse 基站及其状态。

**请求体：**

无。

**响应：**

```text
200 OK
```

示例：

```json
[
  {
    "name": "LHB-STATION1_RENAMED",
    "originalName": "LHB-XXXXXXXX",
    "address": "XX:XX:XX:XX:XX:XX",
    "powerState": 1,
    "channel": 3
  },
  {
    "name": "LHB-YYYYYYYY",
    "originalName": "LHB-YYYYYYYY",
    "address": "YY:YY:YY:YY:YY:YY",
    "powerState": 0,
    "channel": 8
  }
]
```

### 电源状态

```text
-1 = Unknown
 0 = Sleep
 1 = On
 2 = Standby
 3 = Booting
```

### 光学通道

```text
0    = Unknown
1-16 = Lighthouse 光学通道
```

---

## `POST /scan`

触发一次后台 Lighthouse 扫描。

整个过程大约为 5 秒 BLE 扫描，随后执行有界的逐站状态读取。

扫描结束后：

```text
GET /status
```

返回的数据会自动更新。

**请求体：**

无。

**响应：**

成功启动扫描：

```text
202 Accepted
```

当前已有其他蓝牙操作：

```text
409 Conflict
```

---

## `GET /scan/status`

获取当前或最近一次扫描状态。

返回内容包括：

* 扫描状态
* 开始/结束时间
* 警告信息
* 最近一次扫描实际发现的基站数量

---

## `POST /scan/stop`

取消当前正在执行的扫描，并等待扫描工作流结束。

没有扫描任务正在执行时调用该接口同样是安全的。

**响应：**

```text
204 No Content
```

如果取消请求已送达、但扫描工作流在有界等待内仍未排空（例如某个适配器调用忽略了取消），接口会返回：

```text
408 Request Timeout
```

此时取消仍然保持生效，扫描状态最终仍会变为 `cancelled`；调用方可以轮询 `/scan/status` 确认终态。

取消后的最终扫描状态为：

```text
cancelled
```

---

## `POST /stations/power`

批量修改当前已知基站的电源状态。

**请求体：**

开启：

```json
{
  "state": "on"
}
```

待机：

```json
{
  "state": "standby"
}
```

休眠：

```json
{
  "state": "sleep"
}
```

**响应：**

返回每个已知 Lighthouse 基站的结构化操作结果，包括：

* 是否发送命令
* 操作是否成功
* 是否完成状态确认
* 确认失败原因
* 其他错误信息

---

## `POST /stations/:address/power`

修改指定 Lighthouse 基站的电源状态。

**请求体：**

```json
{
  "state": "on"
}
```

或：

```json
{
  "state": "standby"
}
```

或：

```json
{
  "state": "sleep"
}
```

成功时返回：

```text
200 OK
```

响应中包含：

* 更新后的基站信息
* `commandSent`
* `skipped`
* `reason`
* `confirmed`
* `confirmationError`

如果基站在最近一次可信状态读取中已经处于目标状态，则不会发送任何命令，返回：

```json
{
  "commandSent": false,
  "skipped": true,
  "reason": "already at target state",
  "confirmed": true
}
```

此时 `confirmed` 表示“状态已由新鲜读回确认”，而不是“命令已执行并确认”。

如果命令已经成功写入设备，但无法确认最终状态，则会返回：

```json
{
  "commandSent": true,
  "skipped": false,
  "confirmed": false
}
```

命令写入之前发生的错误仍然使用正常的 `4xx` 或 `5xx` HTTP 状态码。

---

## `POST /stations/:address/identify`

让指定 Lighthouse 基站执行 Identify 操作。

设备支持该能力时，会通过 LED 闪烁帮助用户确认实体基站的位置。

---

## `POST /stations/:address/refresh`

强制重新执行 BLE 服务和 Characteristic 发现，并刷新：

* 设备能力
* 元数据
* 电源状态
* 光学通道

---

## `PUT /stations/:address/channel`

修改指定 Lighthouse 基站的光学通道。

**请求体：**

```json
{
  "channel": 5,
  "allowUnknownConflictRisk": false
}
```

有效范围：

```text
1-16
```

修改流程：

1. 检查当前可见基站是否存在目标通道冲突。
2. 存在明显冲突时拒绝修改。
3. 如果一个或多个当前可见基站的通道未知，默认拒绝修改；只有调用方明确将 `allowUnknownConflictRisk` 设为 `true` 时才继续，并在结果中返回警告。
4. 向基站写入新的通道。
5. 重新读取设备通道。
6. 只有回读值与目标值一致时，才认为修改得到完整确认。

如果写入命令已经成功发送，但无法进行回读验证，会返回：

```text
200 OK
```

同时：

```json
{
  "commandSent": true,
  "confirmed": false
}
```

客户端遇到这种情况时**不得自动重试通道写入**，避免重复修改导致设备状态不可预测。

## curl 示例

获取当前状态：

```bash
curl http://127.0.0.1:7575/status
```

开启全部基站：

```bash
curl -X POST http://127.0.0.1:7575/allon
```

关闭全部基站：

```bash
curl -X POST http://127.0.0.1:7575/alloff
```

旧版 `/allon` 与 `/alloff` 别名返回与 `POST /stations/power` 相同的逐站详细结果结构，包括跳过和未确认的结果。

旧版 Wails `PowerOnStation` 与 `PowerOffStation` 方法保持仅返回错误的兼容契约：已发送但无法确认的命令会被接受并记录日志。新集成应使用 `SetStationPower`，以分别获取 `commandSent`、`confirmed` 与确认错误。

## 发布前硬件检查清单

自动化测试无法完整模拟 Windows WinRT 蓝牙栈或实体 Lighthouse 基站。

因此，在发布正式版本之前，应在具有 Bluetooth 功能的 Windows 设备上进行以下验证：

1. 连续执行至少 10 次扫描，确认整个进程保持稳定。
2. 在扫描过程中退出程序，确认应用能够正常关闭且不会崩溃。
3. 分别测试单个基站和全部已知基站的：

   * On
   * Standby
   * Sleep
4. 验证：

   * 电源命令状态回读
   * 通道冲突拦截
   * 成功修改光学通道并完成回读确认
5. 关闭并重新开启 Windows 蓝牙，然后确认：

   * 扫描功能能够恢复
   * 设备控制功能能够恢复

---

# English

A simple application to control Valve Lighthouse (SteamVR) base station v2.0 power states via Bluetooth Low Energy.

`lhcontrol` is a Windows-only application. Its bundled Bluetooth dependency contains Windows WinRT stability patches and is not intended to compile on Linux or macOS.

## Features

* Scan for nearby Lighthouse base stations.
* Display all Lighthouse 2.0 power states:

  * Sleep
  * Standby
  * Booting
  * On
  * Unknown
* Display the optical channel (1-16) reported by Lighthouse 2.0 stations.
* Set individual stations to:

  * On
  * Standby
  * Sleep
* Identify a physical station by flashing its LED when supported.
* Detect channel conflicts and safely change a channel with mandatory readback verification.
* Display:

  * Firmware version
  * Hardware version
  * Model
  * Serial number
  * Manufacturer
  * BLE capabilities
* Set all base stations discovered during the current application session to On, Standby, or Sleep.
* Rename base stations locally for easier identification.
* Use the interface in English or Simplified Chinese. With no saved preference, the application follows the system language; the selection can be changed immediately in Settings and is persisted.
* Keep a persistent list of discovered stations during a single application session, including stations not rediscovered during the latest scan.
* List Bluetooth adapters for radio-availability diagnostics. Windows chooses the radio used for BLE discovery and connections.
* Automatically put stations to sleep after SteamVR (`vrserver.exe`) or Steam (`steam.exe`) exits, with a configurable delay from 1 to 120 minutes.
* Reconcile scans started through the HTTP API and surface scan recovery, API health, configuration persistence, and device-presence states in the UI.
* Enforce a single Windows application instance and bring the existing window forward when launched again.

## Technology Stack

* **Framework:** [Wails v2](https://wails.io/)
* **Backend:** Go
* **Frontend:** Svelte
* **Bluetooth:** [TinyGo Bluetooth Library](https://github.com/tinygo-org/bluetooth)

## Prerequisites

* **Operating system:** Windows 10 or Windows 11, x64.
* **Bluetooth Adapter:** A Windows-compatible Bluetooth Low Energy adapter with current drivers.
* **Go:** Version 1.25.12.
* **Node.js:** Version 22.14 or newer in the Node 22 release line.
* **Wails CLI:**

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
```

* **NSIS:** Version 3.12 is required only when building the Windows installer:

```bash
wails build -nsis
```

## Setup

### 1. Clone the repository

```bash
git clone https://github.com/FlameInTheDark/lhcontrol
cd lhcontrol
```

### 2. Install frontend dependencies

Wails normally handles frontend dependencies automatically during the build, but they can also be installed manually:

```bash
cd frontend
npm ci
cd ..
```

## Testing

The Go root package embeds the generated frontend, so a clean checkout must build `frontend/dist` before running `go test ./...`. Use the repository test entry point to install dependencies, check and test the frontend, build the embedded assets, and run all Go tests:

```powershell
.\scripts\test.ps1
```

On Bash:

```bash
./scripts/test.sh
```

## Running the Application

### Development Mode

Live reload:

```bash
wails dev
```

### Production Build

```bash
wails build
```

The Windows executable is created under:

```text
build/bin
```

A pre-built installer may also be available from the project's Releases:

```text
lhcontrol-amd64-installer.exe
```

## Usage

1. Launch the application.
2. Click **Scan** to discover nearby Lighthouse base stations.
3. The application attempts to connect to discovered stations and determine their current power states.
4. Use the **Power** menu to select:

   * On
   * Standby
   * Sleep
5. Open **Details** to inspect capabilities and metadata, identify a physical station, or safely change its optical channel.
6. Use the **All** controls to change all stations discovered during the current application session, including stations missed by the latest scan.
7. Open **Settings** in the upper-right corner to:

   * Switch between English and Simplified Chinese under **Language**. The change applies immediately and is restored on the next launch. On first launch, or when no preference has been saved, the application follows the Windows system language.
   * Under **Scanning and refresh**, choose whether to scan on startup, set a 2-30 second scan duration, enable or disable automatic station refresh, and select a 5-300 second polling interval. Longer intervals extend display freshness automatically, while power writes keep an independent 45-second safety window and force a read when that window expires.
   * Enable **Auto sleep**, choose SteamVR or Steam as the watched process, and set a delay from 1 to 120 minutes.
   * Set the bulk-operation timeout (30–600 s) and the per-station operation timeout (30–120 s; the bulk timeout must stay at least as large) under **Operation safety**.
   * **Power operation timing**, **Connection timing**, **Channel and presence**, **Read budgets**, and **Background recovery** expose advanced Bluetooth timing settings (confirmation attempts and intervals, retries and backoffs, read budgets, recovery scheduling). The defaults suit typical setups and rarely need changes.
   * Change the local HTTP API listen address under **HTTP API** (the server restarts on the new address after saving), and inspect Windows radios under **Bluetooth diagnostics**.

Auto sleep only arms after the watched process has been observed running. If the process remains closed for the configured delay, the application scans and then puts every known station into Sleep. Relaunching the process during the delay or while automatic sleep is running cancels the action, stops commands not yet sent, and reports how many stations completed, failed, or were skipped before cancellation. A bulk timeout also preserves partial results and separately reports confirmed, unconfirmed, failed, and timeout-skipped stations. If another Bluetooth operation is active when the timer fires, that auto-sleep cycle is skipped without an automatic retry.

## Settings and Local Data

Station aliases, scan and refresh preferences, bulk and per-station operation timeouts, the advanced power/connection/channel timing, recovery, and HTTP API settings, auto-sleep settings, and an explicitly selected interface language are stored in:

```text
%APPDATA%\lhcontrol\config.json
```

If the configuration is invalid, the application preserves a timestamped `config.json.invalid-*` copy and falls back to defaults. The footer reports read-only configuration or persistence failures.

## Troubleshooting

### Scanning Issues

If scanning works the first time but later scans fail, or device interactions fail with errors such as:

```text
characteristic not found
```

try the following:

1. Remove the Lighthouse base station from the Windows Bluetooth device list.
2. Restart the computer.
3. Do not manually re-pair the Lighthouse through Windows Settings.
4. Start `lhcontrol` and allow the application to discover it through BLE scanning.

### Bluetooth unavailable

If Bluetooth is disabled or the adapter is temporarily disconnected:

1. Turn Bluetooth back on or reconnect the adapter.
2. Wait approximately two seconds.
3. Scan again.

The running application retries Bluetooth adapter initialization without requiring a restart.

### Bluetooth Drivers

Make sure the Bluetooth adapter is using current drivers.

### Permissions

Some Windows environments may require appropriate permissions for applications to access Bluetooth hardware.

## Windows Hardware Verification

With the application running, execute:

```powershell
.\scripts\hardware-smoke.ps1
```

The script performs ten scan cycles by default and verifies:

* Scan stability.
* Stable station ordering.
* Expected station discovery.
* Scan duration.
* Raw status and error details for failed scans.
* Request, validation, and error details for failed power operations.

Reports are stored under:

```text
build\verification
```

Use the following parameters to define the hardware acceptance set:

```powershell
-MinimumStations
-ExpectedAddresses
-ScanCycles
```

Station assertions apply only to devices actually discovered in every scan, not historical entries retained by the application session.

### Power Exercise

Only use the following option when it is safe to test all known Lighthouse stations:

```powershell
.\scripts\hardware-smoke.ps1 -ExercisePower
```

The script exercises:

* On
* Standby
* Sleep

It requires confirmed initial states for all stations.

Each operation is validated through readback. The script restores the original states in a `finally` block and exits unsuccessfully if restoration fails.

### Hardware-Free Self Test

Run:

```powershell
.\scripts\hardware-smoke.ps1 -SelfTest
```

This validates the reporting and assertion logic without requiring Bluetooth hardware.

## Diagnostic Log

The diagnostic log is stored at:

```text
%APPDATA%\lhcontrol\lhcontrol.log
```

The log is capped at:

```text
5 MB
```

One previous log segment is retained as:

```text
lhcontrol.log.1
```

---

# HTTP API

For integration with external scripts or applications, `lhcontrol` exposes a simple local HTTP API at:

```text
http://127.0.0.1:7575
```

The API listens on the local loopback address (`127.0.0.1:7575`) by default; the listen address can be changed in Settings (the server restarts on the new address after saving). JSON request bodies are limited to 16 KiB. Errors use the `{"error":"..."}` shape; requests normally return `409 Conflict` while another foreground Bluetooth operation is active.

## `GET /health`

Returns local API and configuration-persistence health:

```json
{
  "running": true,
  "address": "127.0.0.1:7575",
  "error": "",
  "warnings": [],
  "configWritable": true,
  "activeOperations": [],
  "operationRevision": 42
}
```

Use this endpoint for startup probes and diagnostics. `running` describes the API listener, while `warnings` reports configuration load or save problems. `activeOperations` lists the externally visible operations currently in flight (each with an `id` and a `kind`, such as bulk power, single-station power, or auto-sleep; scans run through the separate scan event stream and are not listed here); `operationRevision` is a monotonically increasing operation sequence number that the UI uses to version-gate external station snapshots, and clients merging state should compare it rather than assume a single atomic instant.

---

## `POST /allon`

Attempts to turn ON all base stations known in the current application session.

**Request Body:**

None.

**Response:**

Returns:

```text
200 OK
```

with the detailed per-station result.

Returns:

```text
409 Conflict
```

if another foreground Bluetooth operation is active.

---

## `POST /alloff`

Attempts to put all base stations known in the current application session into Sleep.

**Request Body:**

None.

**Response:**

Returns:

```text
200 OK
```

with the detailed per-station result.

Returns:

```text
409 Conflict
```

if another foreground Bluetooth operation is active.

---

## `GET /status`

Returns the current list of known Lighthouse base stations and their states.

**Request Body:**

None.

**Response:**

```text
200 OK
```

Example:

```json
[
  {
    "name": "LHB-STATION1_RENAMED",
    "originalName": "LHB-XXXXXXXX",
    "address": "XX:XX:XX:XX:XX:XX",
    "powerState": 1,
    "channel": 3
  },
  {
    "name": "LHB-YYYYYYYY",
    "originalName": "LHB-YYYYYYYY",
    "address": "YY:YY:YY:YY:YY:YY",
    "powerState": 0,
    "channel": 8
  }
]
```

### Power States

```text
-1 = Unknown
 0 = Sleep
 1 = On
 2 = Standby
 3 = Booting
```

### Optical Channel

```text
0    = Unknown
1-16 = Optical channel
```

---

## `POST /scan`

Triggers a background scan for Lighthouse base stations.

The workflow takes approximately 5 seconds of BLE scanning, followed by bounded per-station state reads.

The list returned by:

```text
GET /status
```

is updated after the scan completes.

**Request Body:**

None.

**Response:**

```text
202 Accepted
```

when the scan starts.

Returns:

```text
409 Conflict
```

if another Bluetooth operation is active.

---

## `GET /scan/status`

Returns the current or latest scan state, including:

* Scan status
* Timestamps
* Warnings
* Number of stations actually seen during the most recent scan

---

## `POST /scan/stop`

Cancels an active scan and waits for the scan workflow to finish.

It is safe to call when no scan is active.

**Response:**

```text
204 No Content
```

If the cancellation was delivered but the scan workflow does not drain within the bounded wait (for example an adapter call that ignores cancellation), the endpoint returns:

```text
408 Request Timeout
```

The cancellation stays in effect and the scan status still settles to `cancelled`; poll `/scan/status` to observe the terminal state.

The terminal scan status is:

```text
cancelled
```

---

## `POST /stations/power`

Changes the power state of all currently known stations.

**Request Body:**

On:

```json
{
  "state": "on"
}
```

Standby:

```json
{
  "state": "standby"
}
```

Sleep:

```json
{
  "state": "sleep"
}
```

**Response:**

Returns a structured result for every known station, including:

* Whether the command was sent
* Success state
* Confirmation state
* Confirmation errors
* Other operation errors

---

## `POST /stations/:address/power`

Changes the power state of a specific Lighthouse base station.

**Request Body:**

```json
{
  "state": "on"
}
```

or:

```json
{
  "state": "standby"
}
```

or:

```json
{
  "state": "sleep"
}
```

A successful request returns:

```text
200 OK
```

with:

* Updated station information
* `commandSent`
* `skipped`
* `reason`
* `confirmed`
* `confirmationError`

When the station was already at the target state according to a recent
verified read, no command is sent and the response is:

```json
{
  "commandSent": false,
  "skipped": true,
  "reason": "already at target state",
  "confirmed": true
}
```

In this case `confirmed` means "the state was confirmed by a fresh read",
not "a command was executed and confirmed".

A command that was successfully sent but could not be confirmed is represented as:

```json
{
  "commandSent": true,
  "skipped": false,
  "confirmed": false
}
```

Failures that occur before the write retain their normal `4xx` or `5xx` HTTP status.

---

## `POST /stations/:address/identify`

Flashes the selected physical Lighthouse station when the Identify capability is supported.

This can be used to determine which physical station corresponds to a device shown in the application.

---

## `POST /stations/:address/refresh`

Forces BLE service and characteristic discovery and refreshes:

* Capabilities
* Metadata
* Power state
* Optical channel

---

## `PUT /stations/:address/channel`

Changes the optical channel of a specific Lighthouse station.

**Request Body:**

```json
{
  "channel": 5,
  "allowUnknownConflictRisk": false
}
```

Valid range:

```text
1-16
```

The application:

1. Checks visible stations for channel conflicts.
2. Rejects the operation when a visible conflict exists.
3. Rejects the operation by default when one or more visible stations have an unknown channel. Set `allowUnknownConflictRisk` to `true` to proceed explicitly; the result will contain a warning.
4. Writes the new channel.
5. Reads the channel back from the station.
6. Reports full success only when the readback matches the requested value.

If the command was sent successfully but readback is unavailable, the API returns:

```text
200 OK
```

with:

```json
{
  "commandSent": true,
  "confirmed": false
}
```

Clients **must not automatically retry the channel write** in this situation.

## Example Usage with curl

Get current status:

```bash
curl http://127.0.0.1:7575/status
```

Turn all base stations ON:

```bash
curl -X POST http://127.0.0.1:7575/allon
```

Turn all base stations OFF:

```bash
curl -X POST http://127.0.0.1:7575/alloff
```

The legacy `/allon` and `/alloff` aliases return the same detailed per-station result shape as `POST /stations/power`, including skipped and unconfirmed outcomes.

The legacy Wails `PowerOnStation` and `PowerOffStation` methods keep their error-only compatibility contract: a command that was sent but could not be confirmed is accepted and logged. New integrations should use `SetStationPower` to receive `commandSent`, `confirmed`, and the confirmation error separately.

## Release Hardware Checklist

Automated tests cannot fully emulate Windows WinRT or a physical Lighthouse base station.

Before publishing a release on a Bluetooth-enabled Windows machine:

1. Run at least 10 consecutive scans and verify the process remains stable.
2. Exit the application during a scan and confirm shutdown completes without a crash.
3. Exercise On, Standby, and Sleep for:

   * One station
   * All known stations
4. Verify:

   * Command readback
   * Channel conflict rejection
   * A successful channel change with readback confirmation
5. Disable and re-enable Bluetooth, then verify:

   * Scanning recovers
   * Device controls recover
