# Android 客户端（Compose 原生）

当前 Android 客户端为 **Jetpack Compose 原生实现**（`android/app-native/`），不再使用 WebView + `@JavascriptInterface` 的 Web 适配方式。本文档为当前实现的事实参考；历史规划与一次性报告已归档（见 `docs/archive/` 与 `deploy/archive/`）。

> 变更时间线：v0.11.x 期间从 WebView 架构重构为 Compose 原生。旧 WebView 工程
> （`android/app/` + `android/src/android.ts` + `shared/web` Android 产物）已删除。

## 技术栈

- Kotlin + Jetpack Compose（Material 3）
- gomobile AAR（`snetbind` → `libgojni.so`）桥接 Go daemon
- Gradle（`android/gradlew`，模块 `:app-native`）

## 目录结构

```
android/
├── app-native/                    # 主模块
│   ├── build.gradle
│   ├── libs/                      # gomobile AAR（classes.jar + jniLibs）
│   └── src/main/
│       ├── AndroidManifest.xml
│       └── kotlin/com/snet/app/
│           ├── MainActivity.kt    # Compose 宿主、权限请求、QR 扫描
│           ├── SnetApp.kt         # Application（初始化 SnetBridge）
│           ├── SnetBridge.kt      # gomobile 绑定层（所有 Go 调用入口）
│           ├── SnetVpnService.kt  # VPN Service（utun fd、路由、热重启）
│           ├── HardwareID.kt      # 设备 ID 派生（SHA-256）
│           ├── BootReceiver.kt    # BOOT_COMPLETED 开机自启
│           ├── repository/
│           │   └── SnetRepository.kt   # 挂起函数 + Dispatchers.IO 封装
│           ├── ui/
│           │   ├── MainScreen.kt  # 标签页（网络/状态）+ 列表 + 卡片
│           │   ├── NetworkDetailScreen.kt
│           │   ├── NetworkDialogs.kt   # 创建/加入/绑定/成员/设置等对话框
│           │   └── SettingsScreen.kt
│           └── viewmodel/
│               └── MainViewModel.kt
├── build-android.sh               # 一键构建（gomobile bind + gradlew）
├── build.gradle / settings.gradle / gradlew
```

## 构建

```bash
./android/build-android.sh
# 1) gomobile bind -target=android → snet.aar
# 2) 解包 classes.jar 到 app-native/libs, jni/* 到 app-native/jniLibs
# 3) ./gradlew assembleDebug
# 产物: android/app-native/build/outputs/apk/debug/app-native-debug.apk
```

CI（`.github/workflows/build-android.yml`）在 tag `v*` 或 `main` 推送时构建 debug + release APK，
工件路径 `android/app-native/build/outputs/apk/{debug,release}/*.apk`。

## 已实现功能

网络标签页 + 状态标签页（TabRow）；创建/加入/断开/删除网络；二维码加入；
设备授权绑定（ca + code）；网络开关；网络详情、成员列表；子网检测与更新；
设置页；VPN 服务（WireGuard 数据面）、开机自启。

## 关键设计

- **桥接**：`SnetBridge.kt` 通过 gomobile `SnetCore`（`snetbind/snetcore.go`）调用 Go；
  `SnetRepository` 用 `withContext(Dispatchers.IO)` 把阻塞调用移出主线程，UI 经 `StateFlow`
  更新。无 `@JavascriptInterface`、无 `WebBridge`。
- **VPN**：`SnetVpnService` 建立 utun，路由仅隧道 `10.0.0.0/8`，排除协调服务器 IP 防环路；
  `HaltTunnels()` 保留 daemon 保留隧道，`pendingStart` 处理 tunFd 未就绪的热重启。
- **设备 ID**：`Build.MANUFACTURER + Build.MODEL + ANDROID_ID → SHA-256 → 16 hex`（硬件派生）。
- **状态刷新**：`MainViewModel` 轮询 `status()`，5s 间隔。

## 服务端 / 授权联动

- 服务器固定地址 `https://snet.uizhi.eu.org:8090`（`internal/constants`），客户端
  create/join/bind 不再提供服务器地址输入；桌面/Android 在「设置」里用授权码绑定。
- `-require-device-auth` 开启后，未绑定设备无法 create/join/register。

## 相关文档

- 深度旧文档（功能对齐计划、优化计划/报告、重构方案、UI 对齐方案、测试指南/报告、
  WebView 时代产物）→ `docs/archive/` 保留备查。
- 部署与 NAS 更新一次性记录 → `deploy/archive/`。