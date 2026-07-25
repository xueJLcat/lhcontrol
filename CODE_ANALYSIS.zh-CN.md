# lhcontrol 功能与稳定性分析

## 当前能力

- Lighthouse 2.0 五态电源模型：Sleep、Standby、Booting、On、Unknown。
- 可变长度 Channel 读取、1–16 校验、可见设备冲突检测和写后回读确认。
- Capability 探测、Identify、设备厂商/型号/序列号/硬件与固件信息。
- Wails 桌面接口及本机 HTTP API，保留旧电源接口兼容性。
- 扫描可见性防抖：一次漏扫不会立即将设备判为离线。

## 本轮扫描闪退根因

Windows 使用的 TinyGo Bluetooth v0.15.0 扫描实现存在三类进程级风险：

1. WinRT 广播对象、向量和缓冲区未完整检查错误或空指针。
2. `Scan` 清理与定时器 `StopScan` 并发访问、释放同一个 watcher。
3. WinRT 回调或上层扫描回调发生 panic 时没有恢复边界。

项目现在通过本地、最小化的 TinyGo Bluetooth Windows 补丁解决这些问题：

- 串行化 watcher 创建、停止和释放。
- 校验 WinRT 返回值，并安全释放 COM 对象。
- 广播及停止回调增加 nil 检查、单次完成保护和 panic 恢复。
- 扫描、停止扫描、扫描完成事件及各设备工作协程均增加 panic 边界。
- 关闭应用时主动停止扫描，并避免关机后的 Wails 事件发送。
- 生产版始终写入有大小上限的诊断日志：
  `%APPDATA%\lhcontrol\lhcontrol.log`。

## 同步修正的功能问题

- 同一基站的重复请求立即返回 Busy，不再排队阻塞下一次扫描。
- 独立设备 GATT 操作最多并行 2 个；扫描、全量状态读取和批量电源操作保持互斥。
- Channel 写调用报错但回读已达到目标时，按成功处理并返回警告。
- Capability 探测“尚未确认”和“确认不支持”不再混为一谈。
- 连接状态不再因任意一次读写错误被错误显示为连接故障。
- 配置保存串行化；磁盘写入失败时回滚内存中的重命名。
- 旧名称重命名接口会同步已知地址别名，不再出现返回成功但 UI 名称不变。
- 周期状态刷新不再清空电源操作反馈；Identify 失败后会刷新设备详情。
- HTTP API 状态定期刷新，控制按钮在后台状态读取期间禁用。
- 单实例端口被其他程序占用时，不再误判为已有 lhcontrol 窗口。

## 验证结果

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- Bluetooth、Station、Config 测试重复运行 20 次
- 模拟适配器扫描生命周期 100 次/轮
- `svelte-check`
- 前端生产构建
- `npm audit --omit=dev`：0 个漏洞
- Windows EXE 启动后连续触发 8 次扫描失败路径，进程保持存活

当前测试机器的 Windows BLE 无线电返回不可用，因此仍需在开启蓝牙且 Lighthouse
可见的目标机器上做最终实机回归。若仍出现进程退出，请保留上述日志；新版本默认会记录
扫描生命周期和可恢复错误。

## 仍受底层平台限制的边界

TinyGo Bluetooth 的 Windows GATT 连接、服务发现和 characteristic I/O 没有
`context.Context` 取消接口。项目已阻止操作堆积并清理扫描，但极少数系统调用若永久卡住，
上层仍不能强制取消。彻底解决需要上游提供可取消的 WinRT/GATT 接口。
