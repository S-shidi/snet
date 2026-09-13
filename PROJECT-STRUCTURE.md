# SNET 项目深度结构分析

**生成时间**: 2026-09-13  
**项目**: SNET 虚拟组网系统

---

## 📊 项目概览

### 技术栈

| 技术 | 用途 | 文件数 | 主要目录 |
|------|------|--------|----------|
| **Go** | 核心后端（服务器 + 客户端守护进程） | 45 | `internal/`, `cmd/` |
| **Rust** | 桌面客户端（Tauri） | 31 | `desktop/src-tauri/` |
| **Kotlin** | Android 移动客户端 | 6 | `android/app/src/main/kotlin/` |
| **TypeScript/JavaScript** | Web UI（共享） | 2088 | `shared/web/`, `desktop/src/`, `android/app/src/main/assets/web/` |
| **Markdown** | 文档 | 99 | 根目录, `deploy/` |

### 构建输出

| 平台 | 架构 | 二进制文件 | 大小 |
|------|------|-----------|------|
| **macOS** | arm64 | snetd, snetctl | 10MB, 8.2MB |
| **Linux** | amd64 | snetd, snetctl, snet-server | 11MB, 8.6MB, 11MB |
| **Linux** | arm64 | snetd, snetctl | 9.7MB, 7.9MB |
| **Windows** | amd64 | snetd.exe, snetctl.exe, wintun.dll | 11MB, 8.6MB, 418KB |

---

## 📁 目录结构详解

### 根目录

```
/Users/shidi/OpenWork/Snet/
├── .github/              # GitHub Actions CI/CD
│   └── workflows/
│       ├── build-android.yml    # Android APK 自动构建
│       └── build-windows.yml    # Windows 安装包构建
├── .openwork/            # 工作流配置（机器人团队）
│   ├── bots/             # 机器人角色定义
│   │   ├── arch.md       # 架构师
│   │   ├── backend.md    # 后端开发
│   │   ├── devops.md     # DevOps
│   │   ├── frontend.md   # 前端开发
│   │   ├── mobile.md     # 移动开发
│   │   ├── pm.md         # 项目管理
│   │   └── qa.md         # 质量保证
│   ├── hooks/            # Git 钩子
│   ├── logs/             # 工作日志
│   ├── routes/           # 路由配置
│   └── scripts/          # 工作流脚本
├── android/              # Android 移动客户端
│   ├── app/
│   │   ├── build.gradle  # Gradle 配置
│   │   ├── libs/         # gomobile AAR
│   │   └── src/main/
│   │       ├── kotlin/com/snet/app/
│   │       │   ├── MainActivity.kt      # 主界面（WebView）
│   │       │   ├── SnetVpnService.kt    # VPN 服务
│   │       │   ├── SnetBridge.kt        # Go 绑定层
│   │       │   ├── WebBridge.kt         # JS 桥接
│   │       │   ├── HardwareID.kt        # 设备 ID
│   │       │   └── BootReceiver.kt      # 开机启动
│   │       └── assets/web/              # Web UI 资源
│   │           ├── index.html
│   │           ├── app.js
│   │           ├── styles.css
│   │           └── progress.js
│   ├── build.gradle      # 根 Gradle 配置
│   └── settings.gradle
├── build/                # 构建输出
│   ├── darwin-arm64/     # macOS ARM64
│   ├── linux-amd64/      # Linux AMD64
│   ├── linux-arm64/      # Linux ARM64
│   └── windows-amd64/    # Windows AMD64
├── cmd/                  # Go 命令行入口
│   ├── client/
│   │   ├── snetctl/      # 命令行管理工具
│   │   └── snetd/        # 客户端守护进程
│   └── server/
│       └── main.go       # 服务器主程序
├── deploy/               # 部署配置和文档
│   ├── docker/
│   │   ├── Dockerfile
│   │   ├── docker-compose.yml
│   │   └── web/          # Docker Web UI
│   ├── *.sh              # 部署脚本
│   ├── *.md              # 部署文档
│   ├── snet-server.service  # systemd 服务
│   └── phone-android.conf   # 手机 WireGuard 配置
├── desktop/              # Tauri 桌面客户端
│   ├── src/
│   │   ├── main.ts       # TypeScript 入口
│   │   └── desktop.ts
│   ├── src-tauri/
│   │   ├── src/
│   │   │   ├── main.rs   # Rust 入口
│   │   │   ├── lib.rs    # 库入口
│   │   │   └── snet/
│   │   │       ├── snet.rs           # ctl API 封装
│   │   │       └── service/
│   │   │           ├── platform_macos.rs   # macOS launchd
│   │   │           ├── platform_windows.rs # Windows SCM
│   │   │           └── platform_other.rs
│   │   ├── tauri.conf.json  # Tauri 配置
│   │   └── Cargo.toml       # Rust 依赖
│   ├── package.json
│   └── vite.config.ts
├── internal/             # Go 核心代码
│   ├── client/           # 客户端核心
│   │   ├── daemon.go     # 守护进程主逻辑（87KB）
│   │   ├── ctl.go        # 控制 API（15KB）
│   │   ├── control.go    # HTTP 客户端（13KB）
│   │   ├── tunnel.go     # WireGuard 隧道（9KB）
│   │   ├── config.go     # 配置管理
│   │   ├── device.go     # 设备 ID
│   │   └── *.go          # 平台特定代码
│   ├── server/           # 服务器核心
│   │   ├── store.go      # 数据存储（111KB）
│   │   ├── server.go     # HTTP API（42KB）
│   │   ├── relay.go      # UDP 中继（16KB）
│   │   └── probe.go      # 探测服务
│   └── protocol/         # 协议定义
│       ├── types.go      # 数据类型（15KB）
│       └── link.go       # 链接解析
├── scripts/              # 构建和测试脚本
│   ├── build-web.sh      # Web 资源构建
│   ├── update-nas.sh     # NAS 更新脚本
│   ├── e2e.sh            # 端到端测试
│   └── *.sh
├── shared/               # 共享代码
│   └── web/              # 共享 Web UI
│       ├── ui.ts         # UI 逻辑（73KB）
│       ├── types.ts      # 类型定义
│       ├── utils.ts      # 工具函数
│       ├── subnet.ts     # 子网计算
│       ├── index.html    # HTML 模板
│       └── styles.css    # 样式
├── snetbind/             # gomobile 绑定层
│   └── snetcore.go       # Go → Android/Kotlin 绑定
├── DESIGN.md             # 系统设计文档
├── README.md             # 项目说明
├── go.mod                # Go 模块定义
└── go.sum                # Go 依赖锁定
```

---

## 🏗️ 核心架构

### 1. 服务器端 (`internal/server/`)

**主要文件**:
- `store.go` (111KB): 数据存储、BoltDB、并发控制
- `server.go` (42KB): HTTP API、路由、中间件
- `relay.go` (16KB): UDP 中继、懒绑定端口

**核心功能**:
- 网络管理（创建、删除、成员管理）
- 节点注册和认证
- NAT 穿透探测
- UDP 中继转发
- 子网路由广播

**数据存储**:
- BoltDB 嵌入式数据库
- RWMutex 并发控制
- 异步持久化

---

### 2. 客户端守护进程 (`internal/client/`)

**主要文件**:
- `daemon.go` (87KB): 核心逻辑、状态机
- `ctl.go` (15KB): 本地控制 API
- `tunnel.go` (9KB): WireGuard 隧道管理

**核心功能**:
- VPN 隧道建立和维护
- NAT 穿透候选探测
- 直连/中继路径切换
- 网络配置持久化

**平台支持**:
- macOS: launchd 服务
- Windows: SCM 服务
- Linux: systemd 服务
- Android: VPNService API

---

### 3. 协议层 (`internal/protocol/`)

**主要文件**:
- `types.go` (15KB): 数据结构定义

**核心类型**:
```go
type Network struct {
    NetworkID         string
    Name              string
    Subnet            string
    Owner             bool
    ApprovalRequired  bool
    Nodes             []Node
}

type Node struct {
    NodeID          string
    PublicKey       string
    IP              string
    Endpoint        string
    RelayEndpoint   string
    AllowedSubnets  []string
    Online          bool
}
```

---

### 4. 桌面客户端 (`desktop/`)

**技术栈**:
- Tauri v2 (Rust 后端)
- Vite + TypeScript (前端)
- WebView 渲染

**核心功能**:
- 系统托盘集成
- 守护进程自动管理
- 本地控制 API 调用
- 跨平台服务安装

**平台特定**:
- macOS: launchd 服务管理
- Windows: SCM 服务管理
- Linux: systemd 服务管理

---

### 5. Android 客户端 (`android/`)

**技术栈**:
- Kotlin
- WebView UI
- gomobile AAR

**核心组件**:
- `SnetVpnService`: VPN 服务（建立 utun 设备）
- `MainActivity`: WebView 容器
- `SnetBridge`: Go 代码绑定
- `WebBridge`: JavaScript 桥接

**优化特性**:
- 异步 VPN 启动
- 进度反馈
- 立即响应的网络开关
- 自动连接延迟启动

---

### 6. 共享 Web UI (`shared/web/`)

**技术栈**:
- TypeScript
- 无框架原生 JS
- 共享样式

**核心功能**:
- 网络管理界面
- 成员列表
- 邀请链接生成
- 二维码显示
- 子网路由配置

**共享平台**:
- Android WebView
- Docker 容器
- 桌面客户端（可能）

---

## 🔄 数据流

### 网络创建流程

```
用户操作
  ↓
客户端 UI (WebView)
  ↓
WebBridge / SnetBridge
  ↓
HTTP POST /ctl/create
  ↓
守护进程 (snetd)
  ↓
HTTPS POST /api/v1/networks
  ↓
服务器 (snet-server)
  ↓
创建网络 → 分配 IP → 生成 token
  ↓
返回网络信息
  ↓
守护进程保存配置
  ↓
启动 pollLoop
  ↓
建立 WireGuard 隧道
```

### NAT 穿透流程

```
每 15 秒
  ↓
客户端 UDP probe → 服务器 :8091
  ↓
获取公网 IP:port
  ↓
SetEndpoint 广播到服务器
  ↓
服务器分发给其他 peers
  ↓
其他 peers 尝试直连
  ↓
20 秒内握手成功？
  ├─ 是 → 直连模式
  └─ 否 → 回退到 relay
```

---

## 📦 构建系统

### Web 资源构建

```bash
./scripts/build-web.sh
```

**生成目标**:
- `android/app/src/main/assets/web/`
- `deploy/docker/web/`
- `desktop/src/`（可能）

---

### Go 构建

```bash
# macOS
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o build/snetd ./cmd/client/snetd

# Linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/snetd ./cmd/client/snetd

# Windows
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/snetd.exe ./cmd/client/snetd
```

---

### Android 构建

```bash
cd android
./gradlew assembleRelease
```

**输出**: `android/app/build/outputs/apk/release/app-release.apk`

---

### 桌面客户端构建

```bash
cd desktop
npm install
npm run tauri build
```

**输出**: 
- `desktop/src-tauri/target/release/bundle/macos/Snet.app`
- `desktop/src-tauri/target/release/bundle/dmg/Snet_0.1.0_aarch64.dmg`

---

## 🚀 部署配置

### Docker 部署

```yaml
# deploy/docker/docker-compose.yml
services:
  snetd:
    build:
      context: ../..
      dockerfile: deploy/docker/Dockerfile
    container_name: snetd
    privileged: true
    network_mode: host
    volumes:
      - ./data:/usr/local/snet
    restart: unless-stopped
```

---

### systemd 服务

```bash
# deploy/snet-server.service
[Unit]
Description=SNET Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/snet/bin/server
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

---

## 🔐 安全特性

### 服务器安全

- 限流保护
- CSP/HSTS 安全头
- XFF 最右验证
- 凭据加锁
- 引导互斥

### 客户端安全

- ctl-token 认证
- 密钥定期轮换
- 端到端加密（WireGuard）
- 本地密码哈希（bcrypt）

---

## 📊 依赖关系

### Go 依赖

```go
module snet

require (
    go.etcd.io/bbolt v1.5.0          // BoltDB 数据库
    golang.org/x/crypto v0.55.0      // 加密库
    golang.org/x/sys v0.47.0         // 系统调用
    golang.zx2c4.com/wireguard v0.0.0 // WireGuard
)
```

### Rust 依赖

```toml
[dependencies]
tauri = { version = "2", features = ["tray-icon"] }
serde = "1"
serde_json = "1"
ureq = "2"  // HTTP 客户端
```

---

## 🎯 关键设计决策

### 1. 数据存储

**选择**: BoltDB  
**原因**: 嵌入式、无依赖、高性能  
**优化**: RWMutex + 异步持久化

### 2. NAT 穿透

**策略**: 
- 优先直连
- 中继回退
- 候选盲投（对称 NAT）
- 定期重试

### 3. UI 架构

**选择**: WebView + 共享 TypeScript  
**原因**: 
- 跨平台一致
- 代码复用
- 快速迭代

### 4. 客户端架构

**守护进程 + UI 分离**:
- 守护进程: root 权限、VPN 隧道
- UI: 用户空间、图形界面
- 通过 HTTP API 通信

---

## 📝 文档结构

### 核心文档

- `README.md`: 项目说明、快速开始
- `DESIGN.md`: 系统设计、协议定义
- `deploy/*.md`: 部署指南、优化报告

### 部署文档

- `DOCKER-COMPOSE-DEPLOY.md`: Docker 部署
- `SERVER-OPTIMIZATION.md`: 服务器优化
- `CLIENT-ARCHITECTURE.md`: 客户端架构
- `ANDROID-OPTIMIZATION.md`: Android 优化

---

## 🔄 CI/CD 流程

### GitHub Actions

1. **build-android.yml**:
   - 触发: push tag `v*` 或手动
   - 构建: Debug APK + Release APK
   - 上传: GitHub Artifacts

2. **build-windows.yml**:
   - 触发: push tag `v*`
   - 构建: Windows 安装包（NSIS）
   - 上传: GitHub Artifacts

---

## 📈 项目规模

### 代码统计

| 类型 | 文件数 | 主要目录 |
|------|--------|----------|
| Go 源码 | 45 | `internal/`, `cmd/` |
| Rust 源码 | 31 | `desktop/src-tauri/` |
| Kotlin 源码 | 6 | `android/app/src/main/kotlin/` |
| TypeScript | 2088 | `shared/web/`, `desktop/src/` |
| 文档 | 99 | 根目录, `deploy/` |

### 代码行数估算

| 组件 | 行数（估算） |
|------|-------------|
| 服务器 | ~30,000 |
| 客户端守护进程 | ~20,000 |
| 协议层 | ~2,000 |
| Android | ~3,000 |
| 桌面客户端 | ~5,000 |
| Web UI | ~15,000 |
| **总计** | **~75,000+** |

---

## 🎯 总结

### 项目特点

1. **多平台支持**: macOS, Windows, Linux, Android
2. **端到端加密**: WireGuard 隧道
3. **NAT 穿透**: 直连优先，中继回退
4. **共享 UI**: TypeScript 跨平台复用
5. **容器化**: Docker 部署支持
6. **自动化**: GitHub Actions CI/CD

### 技术亮点

- Go 高性能后端
- Rust 安全桌面客户端
- Kotlin 现代 Android
- TypeScript 共享 UI
- WireGuard 安全隧道
- BoltDB 嵌入式存储

### 架构优势

- **模块化**: 客户端/服务器分离
- **可扩展**: 子网路由、多网络
- **高可用**: 中继回退、自动重连
- **易部署**: Docker、systemd、launchd

---

**文档生成时间**: 2026-09-13  
**项目版本**: v0.11.1