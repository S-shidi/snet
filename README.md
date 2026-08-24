# SNET 虚拟组网

端到端加密的私有虚拟局域网（WireGuard 数据面 + HTTPS 协调 + UDP 中继）。

- **服务端**（Go）：网络协调、成员管理、NAT 穿透探测、UDP 中继、Web 管理页（分页列表、总览统计）、设备授权码。
- **客户端**（Go daemon + Tauri 桌面端）：macOS（launchd）、Windows（SCM 服务）、Linux 均支持；
  数据面优先 NAT 打洞直连，打不通时经服务器中继转发。
- **Android 端**（WebView UI + gomobile AAR）：内置 VPN 服务，扫码/链接加入网络。
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
desktop/                 Tauri 桌面端（Rust + Vite/TypeScript）
  src-tauri/src/snet/    ctl 封装与各平台服务安装（macos launchd / windows sc.exe）
deploy/                  部署文档、systemd 服务单元、手机配置
scripts/                 e2e 测试、rollout 脚本、辅助工具
build/                   各平台已编译产物（linux-amd64/arm64、windows-amd64，含 wintun.dll）
.github/workflows/       CI：Windows 安装包构建
```

## 构建

Go（本机任意平台，交叉编译零依赖）。注意 `snetbind`（gomobile 绑定层）当前与
`internal/client` 接口不同步、无法编译，全量 `go build ./...` 会失败；
服务端/客户端按以下范围构建：

```bash
# 服务端 + 客户端（本机）
go build ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl
# Linux（VPS 部署）
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/linux-amd64/... ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl
# Windows daemon（mac 上交叉编译；安装包在 Windows 机器或 CI 构建）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o build/windows-amd64/snetd.exe ./cmd/client/snetd
```

桌面端：

```bash
cd desktop && npm install
npm run tauri dev        # 开发
npm run tauri build      # macOS 出 .app/.dmg
npm run tauri build -- --bundles nsis   # Windows 出安装包（须在 Windows 机器，或走 CI）
```

Android 端（需 JDK 17、Android SDK/NDK、gomobile，路径见脚本内环境变量）：

```bash
./android/build-android.sh   # gomobile bind 出 snet.aar → gradlew assembleDebug
```

## 测试

```bash
go test ./internal/server ./internal/client ./internal/protocol   # Go 单元/e2e 测试
cd desktop/src-tauri && cargo check
```

## 部署 / CI

- 服务端部署、证书、systemd、防火墙、设备授权：见 [`deploy/README.md`](deploy/README.md)。
- **Docker 部署**：支持在 Docker 主机、VPS、群晖 NAS 等环境容器化部署客户端，详见 [`deploy/README.md` 第 10-11 节](deploy/README.md)。
- Windows 安装包：推送后手动触发 `.github/workflows/build-windows.yml`（Actions），
  或打 `v*` tag 自动构建；安装包在 `snet-windows-nsis` artifact 中。
- 服务器 rollout：`scripts/rollout-vps.sh`（本机构建 linux-amd64 并 scp/systemctl rollout）。
