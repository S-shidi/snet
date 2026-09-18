# Android 客户端 UI 对齐 Mac 客户端方案

## 📊 Mac 客户端 UI 分析

### 1. 顶部导航栏
- 品牌标识 "Snet" + 副标题
- daemon 状态指示器（绿色圆点 + "snetd"）
- 刷新、设置、退出按钮

### 2. 标签页导航
- **网络标签页**：显示网络列表
- **状态标签页**：显示系统状态和隧道详情

### 3. 网络页面
- 工具栏：创建、加入按钮
- 网络列表卡片：
  - 网络名称 + ID
  - IP 地址、子网
  - 收发流量统计
  - 状态标签（owner、路由子网）
  - 在线/离线成员
  - 网络开关
  - 更多菜单（⋮）：
    - 详情、成员、邀请、设置、子网路由、配对码、删除

### 4. 状态页面
- 设备 ID
- 服务器地址
- WireGuard 端口
- 隧道详情
- 原始状态 JSON

### 5. 对话框
- 创建网络：名称、子网、描述、标签
- 加入网络：邀请链接 / 网络 ID + 配对码
- 网络设置：名称、子网、审批模式、描述、标签、公开范围
- 邀请成员：邀请链接、配对码、二维码
- 子网路由：添加/删除子网
- 成员管理：角色管理、踢出成员

---

## 🎯 Android 客户端改进方案

### 1. 标签页导航
```kotlin
// 添加 TabRow 实现网络/状态切换
TabRow(selectedTabIndex) {
    Tab(text = { Text("网络") }, ...)
    Tab(text = { Text("状态") }, ...)
}
```

### 2. 网络卡片增强
```kotlin
// 添加流量统计
Text("收 ${formatBytes(network.rxBytes)} · 发 ${formatBytes(network.txBytes)}")

// 添加状态标签
if (network.owner) SuggestionChip(label = "owner")
if (network.allowedSubnets.isNotEmpty()) SuggestionChip(label = "路由 ${network.allowedSubnets.size} 个子网")

// 添加成员统计
Text("${network.onlineCount}/${network.memberCount} 在线")
```

### 3. 网络操作菜单
```kotlin
// 添加完整菜单
DropdownMenu {
    DropdownMenuItem("详情")
    DropdownMenuItem("成员")
    DropdownMenuItem("邀请")
    DropdownMenuItem("设置")
    DropdownMenuItem("子网路由")
    DropdownMenuItem("配对码")
    Divider()
    DropdownMenuItem("删除网络", danger = true)
}
```

### 4. 状态页面
```kotlin
// 添加状态页面
LazyColumn {
    item { StatCard("设备ID", deviceId) }
    item { StatCard("服务器", serverAddr) }
    item { StatCard("WireGuard 端口", wgPort) }
    item { TunnelDetailCard() }
    item { RawJsonCard() }
}
```

---

## 📝 实施计划

### 第一阶段：标签页导航
1. 添加 TabRow
2. 实现网络/状态页面切换
3. 保持当前网络列表功能

### 第二阶段：网络卡片增强
1. 添加流量统计
2. 添加状态标签
3. 添加成员统计
4. 改进网络操作菜单

### 第三阶段：状态页面
1. 创建状态页面
2. 显示设备信息
3. 显示隧道详情
4. 显示原始 JSON

### 第四阶段：完善对话框
1. 创建网络对话框
2. 加入网络对话框
3. 邀请成员对话框（二维码）
4. 网络设置对话框
5. 子网路由对话框

---

## 🚀 开始实施

准备创建完整对齐 Mac 客户端的 Android UI！