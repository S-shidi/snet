# SNET 设计文档

本文档是 SNET 项目的核心设计参考，旨在让任何接手该项目的开发者（包括 AI Bot）能够快速、正确地理解项目全貌。

> **产品定位**：面向个人、家庭及小团队的端到端加密虚拟组网工具。核心要求：简洁、高效、易维护。
> **远期愿景**：社区共享——用户可创建自己的虚拟网并分享给他人，实现资源互通。

---

## 1. 技术架构全景

```
┌─────────────────────────────────────────────────────────────┐
│                    协调服务器 (Go)                             │
│  HTTP API (:8090) + UDP Probe (:8091) + UDP Relay (:51820+) │
│  bbolt 持久化（7 个 bucket）+ go:embed 管理面板                 │
└──────────────────────┬──────────────────────────────────────┘
                       │ HTTPS / JSON REST
        ┌──────────────┼──────────────┐
        ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ Desktop      │ │ Android      │ │ Docker       │
│ (Tauri v2)   │ │ (WebView)    │ │ (snetd+nginx)│
│ Rust 代理    │ │ Kotlin 壳    │ │ Web 控制台   │
│ TS 前端      │ │ Go/JNI 核心  │ │ TS 前端      │
└──────┬───────┘ └──────┬───────┘ └──────────────┘
       │                │
       ▼                ▼
┌──────────────────────────────────┐
│  Go Daemon (snetd)               │
│  WireGuard + NAT 穿透 + 中继回退  │
│  本地 /ctl/* HTTP API (:19432)   │
└──────────────────────────────────┘
```

### 两层控制面

1. **协调控制面**：客户端 ↔ 服务器（HTTPS REST JSON）。负责网络注册、peer 发现、设备绑定。
2. **本地控制面**：UI ↔ snetd（`127.0.0.1:19432` REST JSON）。负责本地 daemon 管理。

### 数据面

- **WireGuard UDP 隧道**：节点间直接通信（或经中继）。
- **UDP Probe**（:8091）：公网 IP 发现，辅助 NAT 穿透。
- **UDP Relay**（:51820+）：每网络独占一端口，作为 NAT 穿透失败时的回退。

---

## 2. 核心业务逻辑

### 2.1 网络生命周期

```
创建网络 → 分配 10.88.0.x/24 → 生成 pairingCode → 返回 token
    ↓
分享 snet://join?nid=xxx&code=yyy[&name=xxx][&server=xxx]
    ↓  <!-- server 参数仅转发用；客户端现只接受固定服务器 https://snet.uizhi.eu.org:8090，异地址链接被拒绝 -->
    ↓
其他设备加入 → 验证 code → 分配 IP + token → 返回 peers 列表
    ↓
NAT 穿透 / 中继回退 → WireGuard 隧道建立 → 资源互通
```

### 2.2 加入与审批

- `ApprovalRequired=false`：知道 code 即可加入（类似 WiFi 密码）
- `ApprovalRequired=true`：join 后进入 pending，owner/admin 审批后生效
- 审批流程复用现有 `PendingNode` 机制

### 2.3 设备身份

- **Android**：`Build.*` + `ANDROID_ID` → SHA-256 → 16 hex（hardware-derived）
- **macOS**：`IOPlatformUUID`
- **Linux**：DMI `product_uuid` → fallback `machine-id`
- **Windows**：注册表 `MachineGuid`
- 身份 + WireGuard 密钥对共存于 `device.id` 文件

### 2.4 NAT 穿透

```
每 15s → 客户端 UDP probe 服务器 :8091 → 获取公网 IP:port
→ SetEndpoint 广播 → peer 尝试直连
→ 20s 内未握手 → fallback 到 relay
→ 300s 后重试直连
```

per-peer 状态机：`direct → relay → retry direct`（v0.10 起每 peer 的当前
路径、直连候选进度对 `/ctl/status` 可见，见 `peerPaths`）。

**对称 NAT 候选盲投**：probe 观测到的公网端口常与对端 WireGuard socket 实际
端口差几个号（对称 NAT 按目标分配连续端口）。direct 模式下 daemon 以
`buildCandidates` 生成「观测端口 + ±1..±8 交错」的 17 个候选，每候选探测
`candProbeSec`（4s），握手成功即锁定该候选，不再切换；预算耗尽仍未握手则
回到 relay。私网段地址（家宽同一 LAN）不做端口扫描，直接单候选。

**健康看门狗（v-quality）**：每网络每 `qualityIntervalSec`（10s）采样各 peer
数据面的 rx/tx 字节增量与握手年龄，`/ctl/status` 经 `peerQuality` 暴露活性。
看门狗只对**当前走 relay** 的 peer 出手：若连 `qualityDeadBatches`（3）次采样
都无流量且握手超过 `directLockStaleSec` 未更新，即判定该 relay 路径已死，强制
立即重选 relay（全部候选重新探 RTT）并复位该 peer 的 direct 重试计时——避免
一个静默死去的 relay 把一个 peer 困到下次定时重探。direct 路径的失效由
`directLockStaleSec` 状态机自行回退，看门狗不越权干预。

### 2.5 子网路由

节点可广播 `allowedSubnets`（CIDR），其他节点通过该节点的隧道访问这些子网。用于共享 NAS、打印机等本地资源。

---

## 3. 目录结构与模块职责

```
cmd/
  server/main.go              服务端入口：flag 解析、store/relay/probe/handler 组装
  client/snetd/main.go        daemon 入口：加载配置、平台特定 runDaemon
  client/snetctl/main.go      CLI 工具：HTTP 客户端调用 /ctl/* API

internal/
  server/
    server.go                 HTTP handler、路由表（~30 端点）、中间件链、Options
    store.go                  bbolt 存储、内存缓存、所有 CRUD 方法（~3100 行）
    relay.go                  UDP 中继（每网络一端口）
    probe.go                  UDP echo（公网 IP 发现）
    admin.html                go:embed 管理面板 SPA

  client/
    daemon.go                 核心协调器（~1935 行）：隧道、轮询、NAT 探测、重试、服务器切换
    ctl.go                    本地 /ctl/* API（24 端点）+ auth 管理
    control.go                协调服务器 HTTP 客户端（apiClient）
    tunnel.go                 WireGuard 隧道管理、路由 diff
    tunnel_*.go               平台特定隧道实现（build tags）
    device.go + device_*.go   设备身份（hardware-derived + 持久化）
    config.go                 配置加载/保存（v2 多网络 schema + v1 迁移）
    crypto.go                 X25519 密钥对生成

  protocol/
    types.go                  共享类型定义（Network, Node, 请求/响应结构体）
    link.go                   snet:// URI 构建与解析

android/
  app/src/main/kotlin/com/snet/app/
    MainActivity.kt           WebView 宿主、权限处理、QR 扫描
    WebBridge.kt              @JavascriptInterface 桥接层（JSON string RPC）
    SnetBridge.kt             gomobile SnetCore wrapper（单例）
    SnetVpnService.kt         VPN 服务（TUN fd 管理、路由排除）
    HardwareID.kt             硬件绑定设备 ID
    BootReceiver.kt           开机自启
  src/android.ts              TypeScript Android 适配器（Backend 实现）

desktop/
  src-tauri/src/
    lib.rs                    应用壳：tray、菜单、窗口管理、Tauri 命令注册
    snet.rs                   HTTP 代理层（22 个 Tauri 命令 → /ctl/* 调用）
    snet/service/             平台服务管理（macOS launchd / Windows SCM）
  src/
    desktop.ts                Tauri 适配器（Backend 实现，通过 Tauri IPC）
    web.ts                    Web 适配器（Backend 实现，通过 HTTP + auth）
    main.ts                   入口（import desktop.ts）

shared/web/
  ui.ts                       核心 UI 控制器（~1474 行，所有渲染/事件/模态框）
  types.ts                    类型定义 + Backend 接口（19 方法）
  utils.ts                    DOM/工具函数（esc, toast, modal, QR）
  subnet.ts                   子网验证与输入组件
  index.html                  共享 HTML 模板
  styles.css                  共享样式

snetbind/
  snetcore.go                 gomobile 绑定层（22 导出方法，JSON string 协议）

scripts/
  build-web.sh                统一 web 资产构建
  e2e.sh                      端到端测试
```

---

## 4. 数据模型

### 4.1 服务器端（bbolt 7 个 bucket）

| Bucket | Key | Value | 说明 |
|--------|-----|-------|------|
| `networks` | networkId | `networkRecord` JSON | 网络基础数据 |
| `nodes` | `netID/nodeID` | `protocol.Node` JSON | 节点数据 |
| `tokens` | SHA-256(rawToken) | `{networkId, nodeId}` JSON | token → 节点映射 |
| `devices` | deviceId | `deviceRecord` JSON | 设备注册信息 |
| `pending` | pendingId | `pendingNode` JSON | 待审批加入请求 |
| `admin` | username | bcrypt hash | 管理员密码 |
| `authcodes` | authCodeId | `authCodeRecord` JSON | 设备授权码 |

**内存 vs 持久化**：所有读操作走内存 map，写操作同时更新内存和 bbolt（节流写入）。

### 4.2 关键结构体

```go
// 服务器端网络记录（持久化格式）
type networkRecord struct {
    ID, OwnerNodeID, CodeHash, Name, Subnet, OwnerDeviceID string
    CreatedAt, LastActivityAt int64
    Remaining, IPAM, RelayPort int
    ExpiresAt, LockedUntil time.Time
    FailCount int
    ApprovalRequired, Managed bool
}

// 协议层网络（API 返回格式）
type Network struct {
    ID, PairingCode, OwnerNodeID, CreatedAt, Name, Subnet, OwnerDeviceID string
    LastActivityAt int64
    RelayPort, NodeCount int
    Online, ApprovalRequired, Managed bool
    PendingCount int
}

// 协议层节点
type Node struct {
    ID, NetworkID, IP, PublicKey, Endpoint, LocalEndpoint string
    LastSeen int64
    DeviceID, DeviceName string
    Online bool
    AllowedSubnets []string
}
```

### 4.3 客户端配置（daemon.json v2）

```go
type Config struct {
    ServerAddr, PrivateKey, DeviceID, ServerCAPath string
    WireguardPort int
    Networks map[string]*NetworkCfg   // nid → 网络配置
    PendingJoins []PendingJoin
    BoundServer, WebPasswordHash string
}

type NetworkCfg struct {
    NodeID, IP, Token, PairingCode, Subnet string
    Port int
    Active, Owner bool
    AllowedSubnets []string
    JoinedAt int64
}
```

---

## 5. API 参考

### 5.1 服务器 REST API（~30 端点）

#### 公开端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/healthz` | 健康检查 |
| POST | `/api/v1/networks` | 创建网络 |
| POST | `/api/v1/networks/{nid}/join` | 加入网络 |
| POST | `/api/v1/devices` | 注册设备 |
| POST | `/api/v1/devices/bind` | 绑定设备 |
| GET | `/api/v1/pending/{pendingID}` | 查询待审批状态 |

#### Token 认证端点

| 方法 | 路径 | 说明 |
|------|------|------|
| PUT | `/api/v1/networks/{nid}/nodes/{nodeID}/endpoint` | 设置 endpoint |
| GET | `/api/v1/networks/{nid}/peers` | 列出 peers |
| PATCH | `/api/v1/networks/{nid}/subnets` | 更新子网路由 |
| DELETE | `/api/v1/networks/{nid}/nodes/{nodeID}` | 移除节点 |
| GET | `/api/v1/networks/{nid}` | 网络详情（owner） |
| PATCH | `/api/v1/networks/{nid}` | 更新网络设置（owner） |
| POST | `/api/v1/networks/{nid}/pending/{pid}/approve` | 批准加入 |
| POST | `/api/v1/networks/{nid}/pending/{pid}/deny` | 拒绝加入 |
| POST | `/api/v1/networks/{nid}/code` | 重置配对码 |
| DELETE | `/api/v1/networks/{nid}/members/{nodeID}` | 踢出成员 |
| DELETE | `/api/v1/networks/{nid}` | 删除网络（owner） |
| POST | `/api/v1/networks/{nid}/claim` | 认领网络 |

#### 管理端点（`/admin/*`，需 admin 认证）

网络管理、设备管理、授权码管理、统计总览等 ~15 个端点。

### 5.2 客户端本地 API（/ctl/* 24 端点）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/ctl/create` | 创建网络 |
| POST | `/ctl/join` | 加入网络 |
| GET | `/ctl/status` | 获取状态 |
| POST | `/ctl/bind` | 绑定服务器 |
| POST | `/ctl/leave` | 断开网络 |
| POST | `/ctl/rejoin` | 重新连接 |
| POST | `/ctl/remove` | 移除网络 |
| POST | `/ctl/rename` | 重命名 |
| POST | `/ctl/settings` | 更新设置 |
| POST | `/ctl/subnets` | 更新子网路由 |
| POST | `/ctl/approve` | 批准加入 |
| POST | `/ctl/deny` | 拒绝加入 |
| POST | `/ctl/cancel-pending` | 取消待审批 |
| POST | `/ctl/delete` | 删除网络 |
| POST | `/ctl/kick` | 踢出成员 |
| POST | `/ctl/reset-code` | 重置配对码 |
| GET | `/ctl/netinfo` | 网络详情 |
| GET | `/ctl/peers` | Peer 列表 |
| GET | `/ctl/local-subnets` | 检测本地子网 |
| POST | `/ctl/device-id` | 获取/设置设备 ID |
| POST | `/ctl/claim` | 认领网络 |
| POST | `/ctl/shutdown` | 关闭 daemon |
| POST | `/ctl/auth/login` | 登录 |
| POST | `/ctl/auth/logout` | 登出 |

### 5.3 gomobile 接口（22 导出方法）

`SnetCore` 结构体，所有复杂数据通过 JSON string 交换（gomobile 类型限制）。

---

## 6. 前端架构

### 6.1 Backend 接口（`shared/web/types.ts`）

```typescript
interface Backend {
  status(): Promise<DaemonStatus | null>;
  create(params): Promise<CreateResp>;
  join(params): Promise<JoinResp>;
  bind(params): Promise<void>;
  rejoin(nid): Promise<void>;
  leave(nid): Promise<void>;
  remove(nid): Promise<void>;
  deleteNet(nid): Promise<void>;
  netinfo(nid): Promise<NetInfoDetail>;
  peers(nid): Promise<PeersResp>;
  updateSettings(params): Promise<void>;
  updateSubnets(params): Promise<void>;
  kick(params): Promise<void>;
  approve(params): Promise<void>;
  deny(params): Promise<void>;
  cancelPending(pid): Promise<void>;
  resetCode(nid): Promise<{pairingCode: string}>;
  detectLocalSubnets(): Promise<string[]>;
  ensureDaemon(): Promise<void>;
  scanQR?(): Promise<string>;         // Android only
  hasDaemonControl: boolean;
}
```

### 6.2 平台适配器

| 平台 | 适配器文件 | Backend 实现方式 | hasDaemonControl |
|------|-----------|-----------------|-----------------|
| Desktop | `desktop/src/desktop.ts` | Tauri IPC → Rust → /ctl/* | true |
| Android | `android/src/android.ts` | @JavascriptInterface → Go/JNI | true |
| Web/Docker | `desktop/src/web.ts` | HTTP /ctl/* + auth | false |

### 6.3 共享 UI（`shared/web/ui.ts`）

- **渲染**：HTML 模板字符串 + `innerHTML`，diff-guard 避免无谓 DOM 更新
- **事件**：容器级委托（`data-act`、`data-nid`），不为每个元素绑定
- **轮询**：5s 间隔，`refresh()` 统一拉取状态并重新渲染
- **乐观 UI**：toggle 开关立即翻转，失败时回滚

---

## 7. 平台实现细节

### 7.1 Desktop（Tauri v2）

- **Rust 端**：纯 HTTP 代理（`ureq` 同步 → `spawn_blocking`），零网络逻辑
- **服务管理**：macOS 用 LaunchDaemon + osascript 提权；Windows 用 SCM + UAC PowerShell
- **窗口行为**：关闭 = 最小化到托盘，双击托盘 = 显示窗口
- **Tray 轮询**：后台线程每 5s 调用 `GET /ctl/status` 更新托盘状态

### 7.2 Android

- **VPN 路由**：只隧道 `10.0.0.0/8`（非全局），保留物理网络访问
- **服务器 IP 排除**：VPN Builder 中排除协调服务器 IP，避免路由环路
- **热重启**：`HaltTunnels()` 保留 daemon 状态，VPN 重建后瞬间恢复
- **线程模型**：`@JavascriptInterface` 方法在后台线程执行，重操作（join/leave/remove）串行化在单线程 executor

### 7.3 Docker 客户端

- snetd + nginx 反代，web 端口 8080
- `--network host` + `--cap-add NET_ADMIN` + `--device /dev/net/tun`
- WG 端口需避开中继池 51820-52075（建议 52100+）

---

## 8. 关键设计决策

| 决策 | 选择 | 理由 |
|------|------|------|
| 数据面 | WireGuard | 性能最优、内核级加密、Linux 原生支持 |
| 协调面 | HTTPS REST JSON | 简单、易调试、跨语言 |
| 中继 | UDP 转发 | 低延迟、与 WireGuard 协议一致 |
| 存储 | bbolt | 嵌入式、零运维、ACID |
| 前端 | 共享 TS + Backend 接口 | 三端复用、维护成本低 |
| 设备 ID | hardware-derived | 跨重装持久、无需用户记忆 |
| NAT 穿透 | server-assisted probe | 不需要额外 STUN 服务器 |
| UI 渲染 | 模板字符串 | 轻量、无框架依赖 |

### 已知局限

- 协调服务器无集群/同步，单点故障（云盘快照备份 `snet.db`）
- daemon.go 过大（~2200 行），职责过重
- 中继二次加密暂无（自托管可信部署，WG 已端到端加密；多租户公服再启用）

> 已解决：`/ctl/*` token 认证、relay 端口池扩大（64→256 + 懒绑定）、
> 协议版本协商、密钥轮换——见「迭代记录」。

---

## 9. 构建与测试

### 构建命令

```bash
# Web 资产（修改 shared/web/ 后必须执行）
./scripts/build-web.sh

# Go
go build ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl

# 测试
go test ./internal/server ./internal/client ./internal/protocol

# Desktop
cd desktop && npm install && npm run tauri dev

# Android
./build-android.sh
```

### 端口规划

| 用途 | 端口 | 协议 |
|------|------|------|
| 协调 API | 8090 | HTTPS/TCP |
| UDP 探测 | 8091 | UDP |
| 中继池 | 51820-52075 | UDP（懒绑定，仅绑活跃网络端口） |
| Docker 客户端 WG | 52100+ | UDP（避开中继池） |
| 本地控制 | 19432 | HTTP/loopback |

### 配置文件路径

| 平台 | 路径 |
|------|------|
| macOS daemon | `/usr/local/snet/daemon.json` |
| Docker | `/data/daemon.json` |
| Windows | `C:\ProgramData\SNET\daemon.json` |
| Android | `/data/data/com.snet.app/files/daemon.json` |
| 客户端配置 | `~/.config/virtual-net/config.json` |

---

## 10. 社区共享：产品设计方向

### 10.1 双层网络模型

- **私有网络**（默认）：现有行为不变，邀请制加入
- **共享网络**（新增）：主动标记为 `shareable`，附带描述/标签，强制审批制

### 10.2 成员角色模型

| 角色 | 权限 |
|------|------|
| owner | 完全控制：设置、踢人、删除、管理角色 |
| admin | 管理：审批加入、踢出成员（不能改设置/删除/管角色） |
| member | 基础：查看成员列表（设备名+IP+在线状态）、访问允许资源 |

### 10.3 数据模型扩展

```go
// Network 新增字段
Description string   `json:"description,omitempty"`
Tags        []string `json:"tags,omitempty"`
Visibility  string   `json:"visibility,omitempty"` // "shareable"

// Node 新增字段
Role string `json:"role,omitempty"` // "owner" | "admin" | "member"

// snet:// 链接扩展（server 参数客户端不再采纳，一律用固定协调服务器）
snet://join?nid=xxx&code=yyy&name=家庭NAS共享[&server=xxx]
```

### 10.4 实施路径

1. **协议层扩展**：types.go 新增字段，link.go 增加 name 参数
2. **服务器存储扩展**：networkRecord/Node 新增字段，新增角色管理方法
3. **服务器 API 扩展**：修改创建/设置端点，新增角色管理端点
4. **客户端 Daemon 扩展**：Create/UpdateSettings 新增参数，新增 SetRole
5. **前端类型扩展**：types.ts Backend 接口新增方法
6. **前端 UI 变更**：创建 modal、设置 modal、成员列表、网络卡片
7. **平台适配器扩展**：desktop.ts、android.ts、Rust、Kotlin

详细执行计划见项目执行记录。

---

## 11. 迭代记录

| 版本 | 内容 |
|------|------|
| v0.11.0 | **服务器地址固定**：协调服务器锁定 `https://snet.uizhi.eu.org:8090`（`internal/constants.DefaultServerAddr`），ctl/snetcore/UI 边界强制校验，create/join/bind 不接受自定义地址，UI 去除服务器输入与明文展示，邀请链接异地址被拒。<br>**授权码过期端到端**：服务端拒绝绑定过期码、`/api/v1/devices/auth-status` 需要 `X-Device-Token`（bound 才返回绑定态）、`Bearer`/管理态返回 `expired`；客户端 `AuthExpired` 置位 + 桌面/Web 过期红卡，管理页支持非 permanent 码续期（本地时区日末）。旧客户端对 bound 设备将收到 401（防探测），升级需服务端+客户端协同。 |
| v0.10.0 | **中继懒绑定 + 池扩容**：relay 端口按网络首次需求才 bind（`Ensure`），池 64→256；启动零 socket 开销，占用失败自动跳下一候选。<br>**协议/API 版本协商**：客户端请求带 `X-Snet-Api-Version`，服务端不匹配回 426（拒绝响应也盖章），无头老客户端兼容；`/healthz` 暴露 `apiVersion/serverVersion`。<br>**路径遥测**：每 peer 的 direct/relay 路径、候选进度暴露到 `/ctl/status`（`peerPaths`）并记录切换日志——同 LAN 直连是否生效一目了然。<br>**对称 NAT 候选盲投**：`buildCandidates` ±1..±8 交错 17 候选轮询，握手锁定。<br>**密钥定期轮换**：`RotateKeys()` 全有或全无（任一网络推送新公钥失败则本地 key 不动），`keyRotationDays>0` 启每日调度；服务端 publickey 端点支持节点 token 双认证。 |
| v0.9.x | `/ctl/*` token + bcrypt 认证；Android VPN 线程安全；mesh IP 上报修正、中继 fan-out（>2 成员可达）、Android 冷启动自动重连。 |
