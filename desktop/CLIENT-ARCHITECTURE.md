# SNET 客户端架构与安装说明

## 🏗️ 架构说明

### 组件关系

```
┌─────────────────────────────────────────────────────────────┐
│                     用户界面层                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │         桌面客户端 (Snet.app)                        │   │
│  │  - GUI 图形界面                                      │   │
│  │  - 网络管理、状态显示                                │   │
│  │  - 用户交互                                          │   │
│  │  - 运行在用户空间（无需 root）                       │   │
│  └──────────────────────────────────────────────────────┘   │
│                          ↓ HTTP API                         │
│                    (127.0.0.1:19432)                        │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                     后台服务层                               │
│  ┌──────────────────────────────────────────────────────┐   │
│  │         后台守护进程 (snetd)                         │   │
│  │  - VPN 隧道管理                                      │   │
│  │  - WireGuard 连接                                    │   │
│  │  - 网络路由、数据转发                                │   │
│  │  - 需要 root 权限（创建 utun 设备）                  │   │
│  │  - 由 LaunchDaemon 管理（自动启动）                  │   │
│  └──────────────────────────────────────────────────────┘   │
│                          ↓ WireGuard                        │
│                    (utun4: 10.88.1.3)                       │
└─────────────────────────────────────────────────────────────┘
```

---

## 🔄 工作流程

### 1. 自动安装流程（推荐）

**用户只需要安装一个桌面客户端**：

```bash
# 安装 Snet.app
open Snet.app
```

**自动流程**：
1. 用户打开 Snet.app
2. 桌面客户端检测守护进程是否运行
3. 如果未运行，弹出 macOS 权限提示（osascript）
4. 用户输入密码授权
5. 自动安装 LaunchDaemon
6. 自动启动 snetd 守护进程
7. 网络连接建立

**关键代码** (`platform_macos.rs:104`):
```rust
pub fn ensure_daemon() -> Result<(), String> {
    ensure_service(
        DAEMON_LABEL,  // com.snet.daemon
        DAEMON_PLIST,  // /Library/LaunchDaemons/com.snet.daemon.plist
        &plist(...),
        &daemon_healthy,
    )?;
    Ok(())
}
```

---

### 2. 手动安装流程（开发者）

如果需要手动安装：

```bash
# 1. 编译守护进程
CGO_ENABLED=0 go build -o snetd ./cmd/client/snetd

# 2. 安装到系统
sudo mkdir -p /usr/local/snet/bin
sudo cp snetd /usr/local/snet/bin/snetd
sudo chmod +x /usr/local/snet/bin/snetd

# 3. 创建配置文件
sudo cp daemon.json /usr/local/snet/daemon.json

# 4. 安装 LaunchDaemon
sudo launchctl bootstrap system /Library/LaunchDaemons/com.snet.daemon.plist

# 5. 安装桌面客户端
open Snet.app
```

---

## 📦 分发方式

### 方案 1: DMG 安装包（推荐）

将所有文件打包成一个 DMG：

```
Snet-Installer.dmg
├── Snet.app              # 桌面客户端
├── Install.sh            # 安装脚本
│   ├── 检查 snetd 是否存在
│   ├── 如果不存在，弹出权限提示安装
│   └── 安装 LaunchDaemon
└── README.txt            # 说明文档
```

**用户操作**：
1. 下载 DMG
2. 打开 DMG
3. 运行 Install.sh 或直接打开 Snet.app

---

### 方案 2: PKG 安装包（企业级）

创建一个标准的 macOS PKG 安装包：

```
Snet-Installer.pkg
├── preinstall            # 安装前脚本
│   ├── 安装 snetd 到 /usr/local/snet/bin/
│   └── 安装 LaunchDaemon
├── Snet.app             # 桌面客户端
└── postinstall          # 安装后脚本
    └── 启动服务
```

**用户操作**：
1. 下载 PKG
2. 双击安装
3. 输入密码授权
4. 完成安装

---

### 方案 3: GitHub Release

在 GitHub Release 中提供：

```
v0.11.1/
├── Snet-Installer.dmg          # 通用安装包
├── Snet.app.tar.gz             # 仅桌面客户端
├── snetd-darwin-arm64          # 仅守护进程
└── checksums.txt               # 校验和
```

---

## ❓ 常见问题

### Q1: 用户需要分别安装两个组件吗？

**A**: **不需要**。用户只需要安装一个桌面客户端，它会自动管理后台守护进程。

---

### Q2: 守护进程在哪里？

**A**: 守护进程位于 `/usr/local/snet/bin/snetd`，由 LaunchDaemon 管理。

**关键路径** (`platform_macos.rs:5-8`):
```rust
const DAEMON_PATH: &str = "/usr/local/snet/bin/snetd";
const DAEMON_LABEL: &str = "com.snet.daemon";
const DAEMON_PLIST: &str = "/Library/LaunchDaemons/com.snet.daemon.plist";
const DAEMON_CONFIG: &str = "/usr/local/snet/daemon.json";
```

---

### Q3: 桌面客户端如何启动守护进程？

**A**: 桌面客户端通过 `osascript` 弹出 macOS 权限提示，用户输入密码后自动安装：

```rust
// platform_macos.rs:11-21
fn run_admin(shell_script: &str) -> Result<(), String> {
    let out = Command::new("/usr/bin/osascript")
        .args(["-e", &format!("do shell script {} with administrator privileges", quote(shell_script))])
        .output()
        .map_err(|e| format!("osascript: {e}"))?;
    ...
}
```

---

### Q4: 如何在其他 Mac 上安装？

**A**: 只需要三个步骤：

**方法 1: 使用安装包（推荐）**
```bash
# 下载 DMG
curl -L https://github.com/S-shidi/snet/releases/download/v0.11.1/Snet-Installer.dmg -o Snet.dmg

# 挂载并安装
open Snet.dmg
# 然后打开 Snet.app，它会自动安装守护进程
```

**方法 2: 从源码构建**
```bash
# 克隆仓库
git clone https://github.com/S-shidi/snet.git
cd snet

# 构建桌面客户端
cd desktop
npm install
npm run tauri build

# 打开应用（会自动安装守护进程）
open src-tauri/target/release/bundle/macos/Snet.app
```

---

### Q5: 守护进程会自动更新吗？

**A**: 会。桌面客户端会检测守护进程版本，如果过旧会自动重新安装。

**检测逻辑** (`platform_macos.rs:80-102`):
```rust
fn ensure_service(...) -> Result<(), String> {
    if healthy() {
        return Ok(());  // 健康则不重装
    }
    // 不健康则重装
    let script = format!(
        "install -m 644 {tmp} {dst} && launchctl bootout system/{label} 2>/dev/null; launchctl bootstrap system {dst}",
        ...
    );
    run_admin(&script)?;
    ...
}
```

---

### Q6: 如何卸载？

**A**: 完整卸载步骤：

```bash
# 1. 停止并卸载守护进程
sudo launchctl bootout system/com.snet.daemon
sudo rm /Library/LaunchDaemons/com.snet.daemon.plist

# 2. 删除守护进程文件
sudo rm -rf /usr/local/snet

# 3. 删除桌面客户端
rm -rf /Applications/Snet.app

# 4. 删除配置和日志
rm -rf ~/Library/Application\ Support/Snet
sudo rm /var/log/snetd.log
```

---

## 🎯 推荐安装方式

### 对于普通用户

**下载 DMG → 打开 Snet.app → 自动完成**

用户不需要关心守护进程，一切自动化。

---

### 对于开发者

**从源码构建**：
```bash
git clone https://github.com/S-shidi/snet.git
cd snet/desktop
npm install
npm run tauri build
open src-tauri/target/release/bundle/macos/Snet.app
```

---

## 📊 对比表

| 项目 | 用户安装 | 开发者安装 |
|------|---------|-----------|
| **需要下载** | 1 个 DMG | 源码 |
| **安装步骤** | 打开 app | 构建 + 打开 app |
| **守护进程** | 自动安装 | 自动安装 |
| **权限提示** | 1 次 | 1 次 |
| **用户感知** | 仅桌面客户端 | 仅桌面客户端 |

---

## 🚀 总结

**关键点**：
1. ✅ **用户只需要安装一个桌面客户端**
2. ✅ **守护进程由桌面客户端自动管理**
3. ✅ **对用户完全透明，无需手动操作**
4. ✅ **支持自动更新和修复**

**技术实现**：
- 桌面客户端通过 `osascript` 请求管理员权限
- 自动安装 LaunchDaemon 到 `/Library/LaunchDaemons/`
- LaunchDaemon 自动启动 snetd 守护进程
- 桌面客户端通过 HTTP API 与守护进程通信

**分发建议**：
- 提供一个 DMG 或 PKG 安装包
- 包含 Snet.app 和安装脚本
- 用户打开 app 即可完成所有安装