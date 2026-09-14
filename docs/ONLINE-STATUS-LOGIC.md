# SNET 在线/离线状态判定逻辑

**生成时间**: 2026-09-13  
**版本**: v0.11.0+

---

## 📊 概述

SNET 服务器端通过 **心跳超时机制** 判断成员和网络是否在线。

---

## 🔧 核心参数

### 服务端配置 (`store.go:63-66`)

```go
lastSeenTTL     = 60 * time.Second // 节点在线判定窗口
netAliveTTL     = 90 * time.Second // 网络在线判定窗口
```

---

## 👥 成员节点在线状态

### 判定逻辑

**条件**: `now - LastSeen < 60秒`

### 代码实现 (`store.go:1646, 2326`)

```go
// 成员节点在线状态判定
n2.Online = now - n2.LastSeen < int64(lastSeenTTL/time.Second)
```

### 工作流程

```
客户端守护进程 (snetd)
    ↓
定期轮询服务器 (poll peers)
    ↓
服务器更新 LastSeen 时间戳
    ↓
判定: 当前时间 - LastSeen < 60秒 ?
    ├─ 是 → 在线 ✅
    └─ 否 → 离线 ❌
```

### 关键点

1. **心跳机制**: 客户端定期调用 `/ctl/peers` API
2. **时间窗口**: 60 秒内的 LastSeen 记为在线
3. **实时更新**: 每次请求都会刷新 LastSeen

---

## 🌐 网络在线状态

### 判定逻辑

**条件**: `now - lastActivityAt < 90秒`

### 代码实现 (`store.go:2315`)

```go
// 网络在线状态判定
n.Online = now - ns.lastActivityAt < int64(netAliveTTL/time.Second)
```

### 工作流程

```
网络中任意成员活动
    ├─ 轮询 peers API
    ├─ 发送 RelayFlow
    ├─ 建立 WireGuard 连接
    └─ 其他网络操作
    ↓
服务器更新 lastActivityAt
    ↓
判定: 当前时间 - lastActivityAt < 90秒 ?
    ├─ 是 → 网络在线 ✅
    └─ 否 → 网络离线 ❌
```

### 关键点

1. **聚合判定**: 网络中任一成员活动都会更新
2. **更长窗口**: 90 秒（比节点的 60 秒更长）
3. **网络级状态**: 反映整个网络的活跃程度

---

## 🎨 Web UI 显示逻辑

### 管理界面 (`admin.html:3574-3579`)

```javascript
// 前端 JavaScript 判定
const online = nd.lastSeen ? (now - nd.lastSeen) < 60 : false;

// 显示 HTML
<span class="pill ${online ? 'online' : 'offline'}">
  ${online ? '在线' : (nd.lastSeen ? '离线' : '活跃（网络级）')}
</span>
```

### 显示规则

| 条件 | 显示 | 样式 | 说明 |
|------|------|------|------|
| `LastSeen < 60秒` | **在线** | 🟢 绿色 pill | 节点活跃 |
| `LastSeen ≥ 60秒` | **离线** | 🔴 红色 pill | 节点失联 |
| `LastSeen = null` | **活跃（网络级）** | 🔴 红色 pill | 无心跳记录 |

---

## 📈 状态流转图

### 成员节点状态流转

```
┌─────────────┐
│  初始状态   │
│ (无LastSeen)│
└──────┬──────┘
       │ 首次轮询
       ↓
┌─────────────┐     60秒内轮询      ┌─────────────┐
│   在线 🟢   │←────────────────────│   在线 🟢   │
│ (LastSeen)  │────────────────────→│ (更新时间戳) │
└──────┬──────┘                     └─────────────┘
       │ >60秒未轮询
       ↓
┌─────────────┐
│   离线 🔴   │
│ (LastSeen)  │
└──────┬──────┘
       │ 再次轮询
       ↓
┌─────────────┐
│   在线 🟢   │
└─────────────┘
```

### 网络状态流转

```
┌─────────────┐
│  网络离线   │
│ (>90秒无活动)│
└──────┬──────┘
       │ 任一成员活动
       ↓
┌─────────────┐     90秒内有活动     ┌─────────────┐
│  网络在线 🟢│←────────────────────│  网络在线 🟢│
│(lastActivityAt)───────────────────→│(更新时间戳) │
└─────────────┘                      └─────────────┘
```

---

## 🔍 实际场景分析

### 场景 1: 群晖 NAS 显示离线

**问题**:
- 群晖 NAS 容器运行中（Up 11 hours）
- 但服务器端显示离线

**原因分析**:
1. 容器启动但没有轮询活动
2. `LastSeen` 时间戳未更新
3. `now - LastSeen ≥ 60秒`

**验证方法**:
```bash
# 检查容器日志
docker logs snet-client | grep "poll peers"

# 应该看到类似：
# [INFO] polling peers from server...
```

**解决方案**:
- 检查守护进程配置
- 确认网络 ID 和 token 正确
- 查看服务器连接日志

---

### 场景 2: Mac 客户端显示在线

**正常流程**:
1. Mac 客户端启动
2. snetd 定期调用 `/ctl/peers`（默认间隔 < 30秒）
3. 服务器更新 `LastSeen`
4. 显示为在线

**验证命令**:
```bash
# 检查 Mac 客户端状态
curl -H "X-Ctl-Token: $(cat /usr/local/snet/ctl-token)" \
  http://127.0.0.1:19432/ctl/status

# 查看服务器端显示
# 应该看到 LastSeen 时间戳在 60 秒内
```

---

## 🛠️ 调试建议

### 1. 检查客户端轮询

```bash
# 查看客户端日志
tail -f /var/log/snetd.log

# 应该定期看到：
# [INFO] polling peers from server...
```

### 2. 检查服务器日志

```bash
# 查看服务器日志
journalctl -u snet-server -f

# 应该看到：
# [INFO] node polling: network=W4NSYT6E node=xxx
```

### 3. 手动触发心跳

```bash
# 手动调用 peers API
curl -H "Authorization: Bearer <token>" \
  https://snet.uizhi.eu.org:8090/api/v1/networks/W4NSYT6E/peers
```

### 4. 检查时间戳

```bash
# 查看节点 LastSeen 时间
# 服务器管理界面 → 网络详情 → 成员列表
# 或通过 API 查询
```

---

## 📝 配置调优

### 调整在线判定窗口

如果网络延迟较高，可以适当延长 TTL：

```go
// store.go:63-66
lastSeenTTL = 120 * time.Second  // 从 60 秒延长到 120 秒
netAliveTTL = 180 * time.Second  // 从 90 秒延长到 180 秒
```

**注意**: 过长的 TTL 会导致离线状态检测延迟。

---

## 🎯 总结

### 核心要点

1. **心跳机制**: 客户端定期轮询 → 服务器更新时间戳
2. **时间窗口**: 节点 60 秒，网络 90 秒
3. **实时判定**: 每次查询时重新计算状态
4. **聚合逻辑**: 网络状态 = 任一成员活动

### 快速诊断

| 问题 | 检查项 | 解决方法 |
|------|--------|----------|
| 节点离线 | 客户端是否轮询？ | 检查 snetd 日志 |
| 网络离线 | 是否有成员活动？ | 检查所有成员状态 |
| 状态闪烁 | 网络是否稳定？ | 检查网络连接质量 |

---

## 📚 相关代码位置

| 功能 | 文件 | 行号 |
|------|------|------|
| TTL 定义 | `internal/server/store.go` | 63-66 |
| 节点在线判定 | `internal/server/store.go` | 1646, 2326 |
| 网络在线判定 | `internal/server/store.go` | 2315 |
| Web UI 显示 | `internal/server/admin.html` | 3574-3579 |
| 管理界面统计 | `internal/server/store.go` | 3423-3426 |