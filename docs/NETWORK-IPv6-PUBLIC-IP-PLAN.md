# 网络成员列表 IPv6 与公网 IP 显示功能规划

## 功能需求

### 1. **虚拟 IP 列扩展**
- 当前只显示虚拟 IPv4
- 新增虚拟 IPv6 显示（如 `fd00:abcd:ef01::1`）

### 2. **新增公网 IP 信息**
- 公网 IPv4（NAT 观察到的地址）
- 公网 IPv6（全局可路由地址）

---

## 实施方案

### Phase 1: 协议层扩展

#### 1.1 **扩展 Node 结构**

**文件：** `internal/protocol/types.go`

```go
type Node struct {
    ID        string `json:"id"`
    NetworkID string `json:"networkId"`
    IP        string `json:"ip"`                  // 虚拟 IPv4 (现有)
    IPv6      string `json:"ipv6,omitempty"`      // 虚拟 IPv6 (新增)
    PublicKey string `json:"publicKey"`
    
    // Endpoint 是 IPv4 公网端点 (NAT 或全局)
    Endpoint  string `json:"endpoint"`
    // EndpointV6 是 IPv6 公网端点 (全局可路由)
    EndpointV6 string `json:"endpointV6,omitempty"`
    
    // PublicIPv4 是设备的公网 IPv4 地址 (不含端口)
    PublicIPv4 string `json:"publicIPv4,omitempty"`  // 新增
    // PublicIPv6 是设备的公网 IPv6 地址 (不含端口)
    PublicIPv6 string `json:"publicIPv6,omitempty"`  // 新增
    
    // ... 其他现有字段保持不变
}
```

**虚拟 IPv6 生成规则：**
- 从网络子网自动推导
- 例如：子网 `10.88.1.0/24` → IPv6 子网 `fd00:88:1::/64`
- IPv4 `10.88.1.5` → IPv6 `fd00:88:1::5`

#### 1.2 **扩展 Device 结构**

**文件：** `internal/protocol/types.go`

```go
type Device struct {
    ID        string `json:"id"`
    PublicKey string `json:"publicKey"`
    CreatedAt string `json:"createdAt"`
    LastSeen  int64  `json:"lastSeen,omitempty"`
    Name      string `json:"name,omitempty"`
    
    // 新增公网 IP 信息
    PublicIPv4 string `json:"publicIPv4,omitempty"`
    PublicIPv6 string `json:"publicIPv6,omitempty"`
}
```

---

### Phase 2: 服务端实现

#### 2.1 **Store 层 - 虚拟 IPv6 生成**

**文件：** `internal/server/store.go`

**新增方法：**
```go
// deriveIPv6FromV4 从 IPv4 推导 IPv6 地址
// 子网 10.88.1.0/24 -> fd00:88:1::/64
// IPv4 10.88.1.5 -> IPv6 fd00:88:1::5
func deriveIPv6FromV4(ipv4 string, subnet string) string {
    // 解析 IPv4 地址
    // 提取子网号：10.88.1 -> fd00:88:1
    // 组合成 IPv6：fd00:88:1::5
}
```

**修改位置：**
- `attachLocked()` - 分配 IP 时同时生成 IPv6
- `Create()` - 创建网络时保存 IPv6 子网信息
- `AdminNodes()` - 返回节点列表时填充 IPv6 字段

#### 2.2 **Store 层 - 公网 IP 记录**

**文件：** `internal/server/store.go`

**修改设备注册逻辑：**
```go
func (s *Store) RegisterDevice(deviceID, publicKey, name string) (*Device, error) {
    // 现有逻辑...
    
    // 从请求中提取公网 IP (HTTP 请求的远程地址)
    // 记录到设备表
    d.PublicIPv4 = extractPublicIP(r.RemoteAddr)
}
```

**修改节点端点更新：**
```go
func (s *Store) SetEndpointV6(...) {
    // 现有逻辑：保存 EndpointV6 (含端口)
    // 新增：提取纯 IPv6 地址保存到 PublicIPv6
    node.PublicIPv6 = extractIPv6FromEndpoint(endpointV6)
}
```

#### 2.3 **API 层 - AdminNodes 返回完整信息**

**文件：** `internal/server/server.go`

**修改 `GET /admin/networks/{nid}/nodes`：**
```go
func (h *handler) handleAdminNodes(w http.ResponseWriter, r *http.Request) {
    nodes := s.AdminNodes(nid)
    
    // 填充每个节点的完整 IP 信息
    for i := range nodes {
        // 虚拟 IPv6
        nodes[i].IPv6 = deriveIPv6FromV4(nodes[i].IP, network.Subnet)
        
        // 公网 IPv4 (从 RelayFlow 或 Endpoint 提取)
        nodes[i].PublicIPv4 = extractIPv4FromEndpoint(nodes[i].RelayFlow)
        
        // 公网 IPv6 (从 EndpointV6 提取)
        nodes[i].PublicIPv6 = extractIPv6FromEndpoint(nodes[i].EndpointV6)
    }
    
    json.NewEncoder(w).Encode(nodes)
}
```

---

### Phase 3: 客户端实现

#### 3.1 **配置层扩展**

**文件：** `internal/client/config.go`

```go
type NetworkCfg struct {
    // ... 现有字段
    PublicIP   string `json:"publicIP,omitempty"`   // IPv4
    PublicIPv6 string `json:"publicIPv6,omitempty"` // 新增
}
```

#### 3.2 **Daemon 层 - 公网 IP 探测**

**文件：** `internal/client/daemon.go`

**修改 `probeSelfPublicIP`：**
```go
func (d *Daemon) probeSelfPublicIP(...) (string, error) {
    // 现有逻辑：返回 IPv4
    
    // 新增：同时探测 IPv6
    // 发送 IPv6 UDP probe
    // 返回 IPv6 地址
}

func (d *Daemon) probeSelfPublicIPv6(...) (string, error) {
    // 使用 IPv6 socket 探测
    // 返回全局 IPv6 地址
}
```

**修改 `advertiseEndpoints`：**
```go
func (d *Daemon) advertiseEndpoints(...) {
    // 现有逻辑：广播 IPv4 endpoint
    
    // 新增：广播 IPv6 endpoint
    if v6, ok := globalIPv6Endpoint(port); ok {
        nc.PublicIPv6 = v6
        api.SetEndpointV6For(nid, nodeID, token, v6)
    }
}
```

---

### Phase 4: 前端实现

#### 4.1 **成员列表 UI 扩展**

**文件：** `internal/server/admin.html`

**修改表头：**
```html
<thead>
  <tr>
    <th>设备</th>
    <th>虚拟 IP</th>          <!-- 原 "IP" 改为 "虚拟 IP" -->
    <th>公网 IP</th>          <!-- 新增 -->
    <th>状态</th>
    <th>子网路由</th>
    <th></th>
  </tr>
</thead>
```

**修改表格行：**
```javascript
rows += `<tr>
  <td class="mono" title="${esc(deviceLabel(nd.deviceName, nd.deviceId))}">
    ${esc(deviceLabel(nd.deviceName, nd.deviceId))}
  </td>
  <td class="mono">
    <div class="ip-cell">
      <span class="ip-v4">${esc(nd.ip)}</span>
      ${nd.ipv6 ? `<span class="ip-v6">${esc(nd.ipv6)}</span>` : ''}
    </div>
  </td>
  <td class="mono">
    <div class="public-ip-cell">
      ${nd.publicIPv4 ? `<span class="ip-v4" title="公网 IPv4">${esc(nd.publicIPv4)}</span>` : '<span class="hint">-</span>'}
      ${nd.publicIPv6 ? `<span class="ip-v6" title="公网 IPv6">${esc(nd.publicIPv6)}</span>` : ''}
    </div>
  </td>
  <td><span class="pill ${online ? 'online' : 'offline'}">${online ? '在线' : '离线'}</span></td>
  <td class="mono">${subnets ? `<span class="subnet-route">${esc(subnets)}</span>` : '<span class="hint">-</span>'}</td>
  <td><button type="button" class="btn danger ghost sm" data-act="kick-node">踢出</button></td>
</tr>`;
```

#### 4.2 **新增样式**

```css
/* IP 单元格 */
.ip-cell, .public-ip-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ip-v4 {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  color: var(--text);
}

.ip-v6 {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: var(--dim);
  opacity: 0.8;
}

/* IP 标签 */
.ip-label {
  display: inline-block;
  padding: 2px 6px;
  border-radius: 3px;
  font-size: 10px;
  font-weight: 500;
  margin-right: 4px;
}

.ip-label.v4 {
  background: var(--accent-soft);
  color: var(--accent2);
}

.ip-label.v6 {
  background: rgba(100, 150, 255, 0.12);
  color: #6496ff;
}
```

---

## 技术细节

### 1. **虚拟 IPv6 生成算法**

```go
func deriveIPv6FromV4(ipv4 string, subnet string) string {
    // 示例输入：
    //   ipv4 = "10.88.1.5"
    //   subnet = "10.88.1.0/24"
    
    // 步骤 1: 解析 IPv4 地址
    parts := strings.Split(ipv4, ".")
    if len(parts) != 4 {
        return ""
    }
    
    // 步骤 2: 提取网络号 (前3段)
    // 10.88.1 -> fd00:88:1
    netParts := parts[:3]  // ["10", "88", "1"]
    
    // 步骤 3: 构造 IPv6 前缀
    // 规则：转为 16 进制
    // 10 -> a, 88 -> 58, 1 -> 1
    prefix := fmt.Sprintf("fd00:%s:%s::", 
        fmt.Sprintf("%x", mustParseInt(netParts[0])),
        fmt.Sprintf("%x", mustParseInt(netParts[1])))
    
    // 步骤 4: 添加主机部分
    // IPv4 最后一段作为 IPv6 最后一段
    host := parts[3]  // "5"
    
    // 结果: fd00:a:58:1::5
    return prefix + host
}
```

**示例映射：**
| IPv4 | 子网 | IPv6 |
|------|------|------|
| 10.88.1.5 | 10.88.1.0/24 | fd00:a:58:1::5 |
| 10.88.1.100 | 10.88.1.0/24 | fd00:a:58:1::100 |
| 10.192.0.15 | 10.192.0.0/24 | fd00:c0:0::15 |

### 2. **公网 IP 提取**

```go
// 从端点字符串提取纯 IP (去除端口)
func extractIPFromEndpoint(endpoint string) string {
    // endpoint 格式: "1.2.3.4:51820" 或 "[fd00::1]:51820"
    
    host, _, err := net.SplitHostPort(endpoint)
    if err != nil {
        return ""
    }
    
    // 去除 IPv6 方括号
    return strings.Trim(host, "[]")
}

// 判断是 IPv4 还是 IPv6
func isIPv6(ip string) bool {
    return strings.Contains(ip, ":")
}
```

### 3. **IPv6 探测逻辑**

```go
func probePublicIPv6(probeAddr string) (string, error) {
    // 使用 IPv6 socket 连接探测服务器
    conn, err := net.Dial("udp6", probeAddr)
    if err != nil {
        return "", err  // 设备没有 IPv6 网络
    }
    defer conn.Close()
    
    // 获取本地地址
    localAddr := conn.LocalAddr().(*net.UDPAddr)
    
    // 过滤掉链路本地地址 (fe80::)
    if localAddr.IP.IsLinkLocalUnicast() {
        return "", errors.New("link-local IPv6")
    }
    
    // 过滤掉私有地址 (fc00::/7, fd00::/8)
    if isPrivateIPv6(localAddr.IP) {
        return "", errors.New("private IPv6")
    }
    
    return localAddr.IP.String(), nil
}
```

---

## 数据库变更

### 无需迁移
- IPv6 从 IPv4 推导，无需存储
- 公网 IP 从现有 `Endpoint` / `EndpointV6` 字段提取
- 前端显示时动态计算

### 可选优化
- 新增 `nodes.ipv6` 字段缓存 IPv6（避免重复计算）
- 新增 `devices.public_ipv4`、`devices.public_ipv6` 字段持久化

---

## 测试计划

### 1. **单元测试**
- `TestDeriveIPv6` - 测试 IPv6 生成算法
- `TestExtractIPFromEndpoint` - 测试 IP 提取
- `TestProbePublicIPv6` - 测试 IPv6 探测

### 2. **集成测试**
- 创建网络，验证 IPv6 自动生成
- 设备注册，验证公网 IP 记录
- 成员列表 API，验证完整 IP 信息返回

### 3. **UI 测试**
- 打开成员列表，验证显示 IPv4 + IPv6
- 验证公网 IP 正确显示
- 验证样式正确

---

## 实施顺序

1. **Phase 1** - 协议层扩展 (1-2小时)
   - 扩展 Node 和 Device 结构
   - 添加 IPv6 生成函数

2. **Phase 2** - 服务端实现 (2-3小时)
   - Store 层逻辑
   - API 层返回完整信息

3. **Phase 3** - 客户端实现 (2-3小时)
   - 配置层扩展
   - IPv6 探测逻辑

4. **Phase 4** - 前端实现 (1-2小时)
   - UI 扩展
   - 样式优化

**总计：** 6-10 小时

---

## 验收标准

✅ 虚拟 IP 列同时显示 IPv4 和 IPv6  
✅ 公网 IP 列显示 IPv4 和 IPv6  
✅ 公网 IP 为空时显示 "-"  
✅ IPv6 地址样式清晰可读  
✅ 不影响现有功能  
✅ 所有测试通过  

---

## 文件清单

### 协议层
- `internal/protocol/types.go` - Node/Device 结构扩展

### 服务端
- `internal/server/store.go` - IPv6 生成、公网 IP 提取
- `internal/server/server.go` - AdminNodes API 修改

### 客户端
- `internal/client/config.go` - NetworkCfg 扩展
- `internal/client/daemon.go` - IPv6 探测

### 前端
- `internal/server/admin.html` - UI 扩展

### 测试
- `internal/server/store_test.go` - IPv6 生成测试
- `internal/client/daemon_test.go` - IPv6 探测测试