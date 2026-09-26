# Store 类拆分实施计划

**生成时间**: 2026-09-16  
**文件**: `internal/server/store.go`  
**当前状态**: 3854 行，111 个方法  

---

## 📊 现状分析

### Store 结构体

```go
type Store struct {
    mu        sync.RWMutex
    db        *bbolt.DB
    networks  map[string]*networkState
    byToken   map[string]tokenEntry
    devices   map[string]*deviceRecord
    authCodes map[string]*authCodeRecord
    netSeq    uint64
    requireDeviceAuth bool
    relayHost  string
    relayBase  int
    relayCount int
    relayEnsure func(port int) error
    nodeEnsure func(port int) error
    relayFlows func(port int, host string) []string
    relaySend func(port int, to string, data []byte) bool
}
```

### 方法分类统计

| 类别 | 方法数 | 占比 |
|------|--------|------|
| 网络操作 | 37 | 33% |
| 节点操作 | 28 | 25% |
| 设备操作 | 25 | 23% |
| 授权码操作 | 8 | 7% |
| 管理员操作 | 30 | 27% |
| 其他辅助方法 | 若干 | - |
| **总计** | **111** | **100%** |

---

## 🎯 拆分方案

### 方案：职责分离（推荐）

#### 文件结构

```
internal/server/
├── store.go              (核心结构，~300 行)
├── store_network.go      (网络操作，~600 行)
├── store_node.go         (节点操作，~500 行)
├── store_device.go       (设备操作，~400 行)
├── store_authcode.go     (授权码操作，~200 行)
├── store_admin.go        (管理员操作，~600 行)
├── store_persist.go      (持久化辅助，~300 行)
└── store_relay.go        (中继相关，~300 行)
```

#### 拆分原则

1. **核心结构保留**：`store.go` 保留 `Store` 结构体定义和初始化
2. **按职责拆分**：每个文件负责一个领域
3. **方法签名不变**：所有方法仍绑定到 `*Store`
4. **共享数据结构**：内部类型定义在 `store.go`

---

## 📋 拆分步骤

### 第一步：创建拆分文件结构

1. 创建 `store_network.go` - 网络相关方法
2. 创建 `store_node.go` - 节点相关方法
3. 创建 `store_device.go` - 设备相关方法
4. 创建 `store_authcode.go` - 授权码相关方法
5. 创建 `store_admin.go` - 管理员相关方法
6. 创建 `store_persist.go` - 持久化辅助方法
7. 创建 `store_relay.go` - 中继相关方法

### 第二步：迁移方法

每个文件迁移对应的方法，保持方法签名不变。

### 第三步：验证

1. 编译测试
2. 单元测试
3. 集成测试

---

## 🔧 详细文件内容

### store.go（核心）

**保留内容**：
- `Store` 结构体定义
- 错误定义
- 常量定义
- 内部类型定义（`networkState`, `tokenEntry` 等）
- 初始化方法（`NewStore`, `Open`）
- 基础辅助方法

**预计行数**：~300 行

---

### store_network.go

**迁移方法**（37 个）：
- `CreateNetwork`
- `ClaimNetwork`
- `UpdateNetworkSettings`
- `persistNetwork`
- `deleteNetworkRows`
- `ensureRelayPort`
- `autoSubnetLocked`
- `touchLocked`
- `touchLockedAsync`
- 其他网络相关方法

**预计行数**：~600 行

---

### store_node.go

**迁移方法**（28 个）：
- `Join`
- `RemoveNode`
- `SetNodeDevice`
- `persistNode`
- `deleteNode`
- `ListPeers`
- `ListPeersFrom`
- `ensureNodePort`
- 其他节点相关方法

**预计行数**：~500 行

---

### store_device.go

**迁移方法**（25 个）：
- `RegisterDevice`
- `upsertDeviceLocked`
- `persistDevice`
- `fillDeviceNameLocked`
- 其他设备相关方法

**预计行数**：~400 行

---

### store_authcode.go

**迁移方法**（8 个）：
- `AdminGenerateAuthCodes`
- `AdminAuthCodes`
- `AdminAuthCodesPage`
- `AdminRevokeAuthCode`
- `persistAuthCode`
- `deleteAuthCode`
- `SetRequireDeviceAuth`
- `RequireDeviceAuth`

**预计行数**：~200 行

---

### store_admin.go

**迁移方法**（30 个）：
- `AdminCreateNetwork`
- `AdminUpdateNetwork`
- `AdminApprove`
- `AdminDeny`
- `AdminNetworkInfo`
- `AdminPending`
- `AdminNetworks`
- `AdminNetworksPage`
- `AdminDevices`
- `AdminDevicesPage`
- 其他管理员方法

**预计行数**：~600 行

---

### store_persist.go

**迁移方法**：
- `persistNetwork`
- `persistNode`
- `persistDevice`
- `persistPending`
- `persistTokenByHash`
- `persistAuthCode`
- 其他持久化相关方法

**预计行数**：~300 行

---

### store_relay.go

**迁移方法**：
- `ensureRelayPort`
- `ensureNodePort`
- `RelayRouteNodePort`
- `SetRelayEnsure`
- `SetNodeEnsure`
- `SetRelayFlowLookup`
- `SetRelaySend`
- 其他中继相关方法

**预计行数**：~300 行

---

## ⚠️ 风险评估

### 高风险项

1. **方法依赖复杂**：部分方法跨领域调用
   - 解决：保持方法在原位置，或提取共享辅助方法

2. **测试覆盖不足**：缺少单元测试
   - 解决：迁移前先运行现有测试，确保通过

3. **锁竞争**：RWMutex 使用可能变化
   - 解决：保持锁使用不变，已在前期优化

### 低风险项

1. **文件组织**：只是代码移动，逻辑不变
2. **编译验证**：Go 编译器会检查一致性

---

## 📊 预期收益

### 可维护性

| 指标 | 优化前 | 优化后 |
|------|--------|--------|
| 单文件行数 | 3854 | ~300-600 |
| 方法数量/文件 | 111 | 10-40 |
| 代码导航 | 困难 | 清晰 |
| 职责分离 | 混乱 | 明确 |

### 开发效率

- ✅ 快速定位相关方法
- ✅ 降低代码审查难度
- ✅ 减少合并冲突
- ✅ 更好的代码组织

---

## 🚀 实施计划

### 阶段 1：准备工作（1小时）

1. ✅ 分析方法分类
2. ✅ 确认依赖关系
3. ✅ 创建文件结构

### 阶段 2：拆分迁移（4-6小时）

1. 创建 `store_network.go`
2. 创建 `store_node.go`
3. 创建 `store_device.go`
4. 创建 `store_authcode.go`
5. 创建 `store_admin.go`
6. 创建 `store_persist.go`
7. 创建 `store_relay.go`

### 阶段 3：验证测试（2小时）

1. 编译测试
2. 单元测试
3. 集成测试
4. 性能基准测试

### 阶段 4：文档更新（1小时）

1. 更新代码注释
2. 更新技术债报告
3. 更新项目结构文档

---

## ✅ 实施原则

### DO（应该做）

- ✅ 保持方法签名不变
- ✅ 保持锁使用不变
- ✅ 保持测试通过
- ✅ 小步迁移，频繁提交
- ✅ 每次迁移后编译验证

### DON'T（不应该做）

- ❌ 改变方法逻辑
- ❌ 改变锁使用方式
- ❌ 一次性大规模迁移
- ❌ 跳过测试验证
- ❌ 忽略编译警告

---

## 📝 当前状态

### 待开始

- 📋 创建拆分文件
- 📋 迁移方法
- 📋 验证测试

---

**准备开始实施 Store 类拆分！**