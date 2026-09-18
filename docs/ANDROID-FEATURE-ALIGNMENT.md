# Android 原生客户端功能对齐计划

## 📊 功能对比：Mac/Windows vs Android 原生

### ✅ 已实现功能

| 功能模块 | Mac/Windows | Android 原生 | 状态 |
|---------|-------------|--------------|------|
| **网络列表** | ✅ 完整 | ✅ 基础 | 已实现 |
| **网络开关** | ✅ 完整 | ✅ 基础 | 已实现 |
| **创建网络** | ✅ 完整 | ✅ 基础 | 已实现 |
| **加入网络** | ✅ 完整 | ✅ 基础 | 已实现 |
| **设备绑定** | ✅ 完整 | ✅ 基础 | 已实现 |
| **VPN 服务** | ✅ 完整 | ✅ 基础 | 已实现 |
| **设备信息** | ✅ 完整 | ✅ 基础 | 已实现 |

### ⏳ 待实现功能

| 功能模块 | Mac/Windows | Android 原生 | 优先级 | 预计工时 |
|---------|-------------|--------------|--------|----------|
| **网络详情** | ✅ | ❌ | P1 | 2h |
| **成员管理** | ✅ | ❌ | P1 | 3h |
| **邀请功能** | ✅ | ❌ | P1 | 2h |
| **网络设置** | ✅ | ❌ | P1 | 2h |
| **子网路由** | ✅ | ❌ | P1 | 2h |
| **配对码管理** | ✅ | ❌ | P1 | 1h |
| **设备授权** | ✅ | ❌ | P1 | 2h |
| **网络删除** | ✅ | ❌ | P1 | 1h |
| **状态详情** | ✅ | ❌ | P2 | 1h |
| **隧道详情** | ✅ | ❌ | P2 | 1h |

---

## 🎯 功能详细说明

### 1. 网络详情（Network Details）

**Mac/Windows 功能**：
- 显示网络 ID、名称、子网
- 显示 IP 地址、状态
- 显示设备数量、在线成员
- 显示收发流量统计
- 显示路由子网列表

**Android 实现**：
- 创建 `NetworkDetailScreen`
- 显示网络完整信息
- 显示成员列表
- 显示流量统计

---

### 2. 成员管理（Members Management）

**Mac/Windows 功能**：
- 查看所有成员（IP、设备名、状态）
- 成员角色管理（owner/admin/member）
- 踢出成员
- 批准/拒绝加入请求
- 查看待审批列表

**Android 实现**：
- 创建 `MembersScreen`
- 成员列表显示
- 角色下拉选择
- 踢出成员按钮
- 待审批处理

---

### 3. 邀请功能（Invite）

**Mac/Windows 功能**：
- 生成邀请链接
- 生成配对码
- 显示二维码
- 复制邀请链接
- 重置配对码

**Android 实现**：
- 创建 `InviteDialog`
- 显示邀请链接和配对码
- 生成二维码（使用 ZXing 库）
- 复制功能
- 重置配对码

---

### 4. 网络设置（Network Settings）

**Mac/Windows 功能**：
- 修改网络名称
- 修改子网（重新分配 IP）
- 开启/关闭审批模式
- 设置描述和标签
- 设置公开范围

**Android 实现**：
- 创建 `NetworkSettingsDialog`
- 所有设置字段
- 子网验证
- 保存功能

---

### 5. 子网路由（Subnet Routes）

**Mac/Windows 功能**：
- 宣告本地子网
- 检测本地子网
- 添加/删除子网路由
- 显示已声明子网

**Android 实现**：
- 创建 `SubnetRoutesDialog`
- 子网输入和验证
- 本地子网检测
- 子网列表管理

---

### 6. 配对码管理（Pairing Code）

**Mac/Windows 功能**：
- 查看配对码
- 重置配对码
- 显示有效次数和时限

**Android 实现**：
- 创建 `PairingCodeDialog`
- 显示配对码
- 重置功能
- 复制功能

---

### 7. 设备授权（Device Approval）

**Mac/Windows 功能**：
- 查看待审批设备
- 批准/拒绝设备
- 显示设备公钥

**Android 实现**：
- 创建 `PendingDevicesDialog`
- 待审批列表
- 批准/拒绝按钮

---

### 8. 网络删除/退出（Delete/Leave）

**Mac/Windows 功能**：
- 删除网络（owner）
- 退出网络（member）
- 确认对话框

**Android 实现**：
- 删除网络按钮
- 退出网络按钮
- 确认对话框

---

## 🚀 实施计划

### 第一阶段（本周）- 完善核心功能

**Day 1-2**（8小时）：
1. ✅ 网络详情（2h）
2. ✅ 成员管理（3h）
3. ✅ 邀请功能（2h）
4. ✅ 配对码管理（1h）

**Day 3-4**（8小时）：
5. ✅ 网络设置（2h）
6. ✅ 子网路由（2h）
7. ✅ 设备授权（2h）
8. ✅ 网络删除/退出（1h）
9. ✅ 状态详情（1h）

---

### 第二阶段（下周）- 优化和完善

**Day 5-6**（6小时）：
1. UI/UX 优化
2. 错误处理完善
3. 性能优化
4. 测试和修复

---

## 📋 技术实现要点

### 1. 数据模型扩展

```kotlin
// 扩展 Network 模型
data class Network(
    val networkId: String,
    val name: String,
    val subnet: String,
    val ip: String?,
    val isActive: Boolean,
    val owner: Boolean,
    val memberCount: Int,
    val onlineCount: Int,
    val rxBytes: Long,
    val txBytes: Long,
    val allowedSubnets: List<String>,
    val description: String?,
    val tags: List<String>,
    val visibility: String?,
    val approvalRequired: Boolean,
    val serverState: String?
)

// 新增成员模型
data class Member(
    val id: String,
    val deviceId: String?,
    val deviceName: String?,
    val ip: String,
    val publicKey: String,
    val online: Boolean,
    val role: String, // owner/admin/member
    val allowedSubnets: List<String>
)

// 新增待审批模型
data class PendingDevice(
    val id: String,
    val deviceId: String?,
    val publicKey: String,
    val error: String?
)
```

### 2. Repository 扩展

```kotlin
// SnetRepository.kt 新增方法
suspend fun getNetworkInfo(networkId: String): NetworkInfo
suspend fun getMembers(networkId: String): List<Member>
suspend fun getPendingDevices(networkId: String): List<PendingDevice>
suspend fun approveDevice(networkId: String, deviceId: String)
suspend fun denyDevice(networkId: String, deviceId: String)
suspend fun kickMember(networkId: String, memberId: String)
suspend fun updateMemberRole(networkId: String, memberId: String, role: String)
suspend fun updateNetworkSettings(networkId: String, settings: NetworkSettings)
suspend fun updateSubnets(networkId: String, subnets: List<String>)
suspend fun resetPairingCode(networkId: String): String
suspend fun getPairingCode(networkId: String): String
suspend fun deleteNetwork(networkId: String)
suspend fun leaveNetwork(networkId: String)
```

### 3. UI 组件

```kotlin
// 新增屏幕和对话框
- NetworkDetailScreen
- MembersScreen
- InviteDialog
- NetworkSettingsDialog
- SubnetRoutesDialog
- PairingCodeDialog
- PendingDevicesDialog
- ConfirmDeleteDialog
```

### 4. 二维码生成

```kotlin
// 使用 ZXing 库生成二维码
dependencies {
    implementation("com.google.zxing:core:3.5.2")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
}
```

---

## 🎯 验收标准

### 功能完整性

- ✅ 所有 Mac/Windows 功能在 Android 上可用
- ✅ 操作流程一致
- ✅ 数据显示一致

### 性能要求

- ✅ 启动时间 < 1秒
- ✅ 界面响应 < 100ms
- ✅ 网络操作 < 3秒

### 用户体验

- ✅ Material Design 3 风格
- ✅ 流畅动画
- ✅ 清晰的反馈
- ✅ 错误提示友好

---

## 📊 工作量估算

**总计**: 20小时（约 2-3 天）

- **核心功能**: 14小时
- **优化完善**: 4小时
- **测试修复**: 2小时

---

**准备就绪后，我将立即开始实施！** 🚀