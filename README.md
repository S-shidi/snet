# SNET 虚拟组网

端到端加密的私有虚拟局域网（WireGuard 数据面 + HTTPS 协调 + UDP 中继）。

- **服务端**（Go）：网络协调、成员管理、NAT 穿透探测、UDP 中继、Web 管理页（分页列表、总览统计）、设备授权码、设备命名。安全加固：限流、安全头（CSP/HSTS/no-store）、XFF 最右、凭据加锁、引导互斥。
- **客户端**（Go daemon + Tauri 桌面端）：macOS（launchd）、Windows（SCM 服务）、Linux 均支持；
  数据面优先 NAT 打洞直连，打不通时经服务器中继转发。设备名自动上报（首次连接填充，管理端改名优先）。
- **Android 端**（WebView UI + gomobile AAR）：内置 VPN 服务，扫码/链接加入网络。设备名取 manufacturer+model。
- **Docker 客户端**：支持在 Docker 主机、VPS、群晖 NAS 等环境容器化部署客户端。
- **手机**：服务器端生成节点配置，用 WireGuard App 导入（见 `deploy/phone-android.conf` 模板）。

## 目录结构

```
cmd/                     Go 入口
  server/                服务端主程序
  client/snetd/          daemon（macOS launchd / Windows 服务 / Linux 前台）
  client/snetctl/        命令行管理工具
internal/
  server/                HTTP API、中继、探测、管理页（admin.html 经 go:embed 内嵌）
  client/                daemon 核心：隧道（ifconfig/netsh）、设备 ID、ctl 接口
  protocol/              客户端-服务端协议类型
android/                 Android 客户端（Gradle 工程，WebView UI + gomobile AAR）
  app/src/main/kotlin/com/snet/app/
    HardwareID.kt        Android 设备硬件 ID 派生
    SnetBridge.kt        gomobile 绑定层（设备名上报、VPN 控制）
  app/src/main/assets/web/  Android 端 web 资产（由 build-web.sh 生成）
desktop/                 Tauri 桌面端（Rust + Vite/TypeScript）
  src-tauri/src/snet/    ctl 封装与各平台服务安装（macos launchd / windows sc.exe）
deploy/                  部署文档、systemd 服务单元、手机配置
  docker/                Docker 部署配置（Dockerfile、entrypoint、nginx、compose）
    web/                 Docker 端 web 资产（由 build-web.sh 生成）
shared/web/              共享 TypeScript UI 源码（ui.ts、types.ts、utils.ts、subnet.ts）
                         index.html、styles.css 为 Android/Docker 共享的 HTML/CSS
snetbind/                gomobile 绑定层（SetDeviceName、ensureDaemon）
scripts/                 构建、部署、e2e 测试脚本
  build-web.sh           统一 web 资产构建脚本（所有平台共享）
build/                   各平台已编译产物（linux-amd64/arm64、windows-amd64，含 wintun.dll）
.github/workflows/       CI：Windows 安装包构建
```

## 构建

### Web 资产（所有平台共享 UI）

**重要**：修改 `shared/web/` 中的 TypeScript 源码后，必须运行此脚本同步所有平台：

```bash
./scripts/build-web.sh
```

此脚本会：
- 从 `android/src/android.ts` 编译 Android `app.js` → `android/app/src/main/assets/web/`
- 从 `desktop/src/web.ts` 编译 Docker `app.js` → `deploy/docker/web/`
- 同步 `index.html` + `styles.css` 到两个平台

### Go（本机任意平台，交叉编译零依赖）

```bash
# 服务端 + 客户端（本机）
go build ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl

# Linux（VPS 部署）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/linux-amd64/... ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl

# Windows daemon（mac 上交叉编译；安装包在 Windows 机器或 CI 构建）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o build/windows-amd64/snetd.exe ./cmd/client/snetd
```

### 桌面端（Tauri）

```bash
cd desktop && npm install
npm run tauri dev        # 开发
npm run tauri build      # macOS 出 .app/.dmg
npm run tauri build -- --bundles nsis   # Windows 出安装包（须在 Windows 机器，或走 CI）
```

### Android 端（需 JDK 17、Android SDK/NDK、gomobile）

```bash
./android/build-android.sh   # gomobile bind 出 snet.aar → gradlew assembleDebug
```

构建脚本会自动调用 `build-web.sh` 同步 web 资产。

## 测试

```bash
go test ./internal/server ./internal/client ./internal/protocol   # Go 单元/e2e 测试
cd desktop/src-tauri && cargo check
```

## 部署

### 服务端部署

见 [`deploy/README.md`](deploy/README.md)。

### Docker 客户端部署

支持在 Docker 主机、VPS、群晖 NAS 等环境容器化部署客户端。

**VPS 部署要点**：
1. 服务端和客户端不能使用相同端口（51820-51883 为服务端 relay 范围）
2. Docker 客户端需使用 `--network host` + `--cap-add NET_ADMIN` + `--device /dev/net/tun`
3. 客户端 WG 端口需配置在 relay 范围之外（如 51900）

**构建 Docker 镜像**：
```bash
cd deploy/docker
docker build -t snetd:latest .
```

**运行 Docker 客户端**：
```bash
docker run -d --name snetd --network host \
  --cap-add NET_ADMIN --cap-add SYS_MODULE \
  --device /dev/net/tun:/dev/net/tun \
  -v /opt/snet/data:/data \
  -v /etc/letsencrypt:/host-certs:ro \
  --restart unless-stopped \
  snetd:latest
```

### Windows 安装包

推送后手动触发 `.github/workflows/build-windows.yml`（Actions），
或打 `v*` tag 自动构建；安装包在 `snet-windows-nsis` artifact 中。

### 服务器 rollout

`scripts/rollout-vps.sh`（本机构建 linux-amd64 并 scp/systemctl rollout）。

## 重要注意事项

1. **子网路由**：客户端可通过控制 API 设置 `allowedSubnets` 广播子网路由，需启用 IP 转发
2. **设备 ID**：
   - Android：硬件绑定（Build.* + ANDROID_ID → SHA-256），仅硬件变更时改变
   - 其他平台：文件持久化（`device.id`），支持重装恢复
3. **端口规划**：
   - 服务端：8090（HTTPS API）、8091（探测）、51820-51883（relay）
   - 客户端 Docker：51900+（WG 端口，避免与服务端冲突）
4. **配置文件路径**：
   - macOS：`/usr/local/snet/daemon.json`
   - Docker：`/data/daemon.json`（通过 volume 持久化）
5. **死锁修复**：`hostname()` 不能在持有 `d.mu.Lock()` 时调用，需使用 `hostnameLocked()` 变体
