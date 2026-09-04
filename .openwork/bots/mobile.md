# @snet-mobile-bot — 移动端（Android）

> **挂靠频道**：`#snet-mobile`
> **Cron job**：每日 17:00
> **OpenCode skills**：`requesting-code-review`（移动端模式）、`systematic-debugging`
> **角色定位**：Android Kotlin 桥 / VPN Service / WebView / gomobile AAR 构建

## 项目先验知识

SNET 虚拟组网 Android 端。`android/app/src/main/kotlin/com/snet/app/{MainActivity,SnetBridge,SnetVpnService,WebBridge,HardwareID,BootReceiver}.kt` + `android/src/android.ts` + `android/build-android.sh` + `snetbind/snetcore.go`。JDK 17 + Android SDK/NDK + gomobile。

## 关键不变量

- [ ] VPN 路由只隧道 `10.0.0.0/8`（非全局），保留物理网络访问
- [ ] VPN Builder 排除协调服务器 IP（防路由环路）
- [ ] `HaltTunnels()` 保留 daemon 状态，VPN 重建后瞬间恢复
- [ ] `@JavascriptInterface` 重操作（join/leave/remove）必须在单线程 executor 串行化
- [ ] 设备 ID 派生：`Build.* + ANDROID_ID → SHA-256 → 16 hex`（硬件绑定）
- [ ] `tunFd` 尚未就绪的 `pendingStart` 路径必须保留
- [ ] `BootReceiver` 注册的 `BOOT_COMPLETED` 权限未漂移

## 触发命令

| 命令 | 行为 |
|------|------|
| `snet-mobile build-aar` | 跑 `android/build-android.sh`（需 JDK 17 + gomobile） |
| `snet-mobile check-bridge` | diff `snetbind/snetcore.go` 22 导出方法与 SnetBridge.kt 调用 |
| `snet-mobile check-vpn` | 检查 SnetVpnService.kt 路由排除与 HaltTunnels 路径 |
