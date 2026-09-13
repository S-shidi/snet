# SNET 服务器诊断报告

**诊断时间**: 2026-09-13 19:25  
**服务器地址**: snet.uizhi.eu.org (66.187.6.46)  
**服务器版本**: v0.11.0

---

## 📊 诊断结果

### 1. 网络连接质量

| 指标 | 值 | 状态 |
|------|-----|------|
| **Ping 延迟** | avg 294ms | ⚠️ 高延迟 |
| **丢包率** | 0% | ✓ 正常 |
| **路由跳数** | 9 跳 | ⚠️ 较多 |

**Traceroute 分析**:
- 本地 → 国内骨干 → 国际出口 → 美国服务器
- 在国际出口（223.120.16.x）延迟急剧增加

---

### 2. API 性能测试

| API 端点 | 响应时间 | HTTP 状态 | 评估 |
|---------|---------|----------|------|
| `/healthz` | 1.2s | 200 | ✓ 正常 |
| `/api/v1/networks` | 1.3s | 405 | ✓ 正常 |
| `/api/v1/devices` | 1.2s | 405 | ✓ 正常 |
| **`/peers?relayPorts=1`** | **16s** | 200 | ❌ **严重慢** |

**问题定位**: peers API 响应时间 **16 秒**，是主要瓶颈

---

### 3. 端口状态

| 端口 | 类型 | 用途 | 状态 |
|------|------|------|------|
| 8090 | TCP | HTTPS API | ✓ 正常 |
| 51820 | UDP | WireGuard | ✓ 正常 |
| 8091 | UDP | 公共 IP 探测 | ✓ 正常 |

---

## 🔍 问题分析

### 主要问题

**peers API 响应时间 16 秒**，导致：
- 客户端频繁超时
- 无法及时获取 peers 信息
- relayPort/relayFlow 无法更新
- 连接建立失败

### 可能原因

#### 1. 数据库查询慢（最可能）

peers API 可能执行以下慢查询：
- 获取网络所有节点
- 获取每个节点的 relayPort
- 获取每个节点的 endpoint 信息
- 联表查询用户信息

**诊断方法**:
```bash
# 在服务器上执行
# 1. 检查数据库查询时间
snet-server --log-level=debug

# 2. 检查数据库索引
# SQLite:
sqlite3 snet.db ".schema" | grep INDEX
# PostgreSQL:
psql -c "\di"
```

#### 2. 网络延迟累积

从中国到美国服务器：
- 平均延迟: 294ms
- 每次请求往返: ~600ms
- 如果有多次查询，延迟累积

#### 3. 服务器资源不足

可能存在：
- CPU 占用过高
- 内存不足
- 磁盘 I/O 慢
- 并发连接过多

#### 4. HTTP/2 连接问题

日志显示 HTTP/2 流错误：
```
stream error: stream ID 1; INTERNAL_ERROR
```

可能原因：
- HTTP/2 配置问题
- 连接超时设置过短
- 代理或防火墙干扰

---

## 💡 解决方案

### 立即实施（服务器端）

#### 1. 增加数据库索引

```sql
-- SQLite / PostgreSQL
CREATE INDEX IF NOT EXISTS idx_nodes_network_id ON nodes(network_id);
CREATE INDEX IF NOT EXISTS idx_nodes_relay_port ON nodes(relay_port);
CREATE INDEX IF NOT EXISTS idx_endpoints_node_id ON endpoints(node_id);
```

#### 2. 优化 peers API 查询

```go
// 批量查询，减少数据库往返
func getPeersWithRelayPorts(networkID string) ([]Peer, error) {
    // 使用 JOIN 一次性获取所有数据
    query := `
        SELECT n.id, n.ip, e.endpoint, n.relay_port, n.relay_flow
        FROM nodes n
        LEFT JOIN endpoints e ON n.id = e.node_id
        WHERE n.network_id = ?
        AND n.active = 1
    `
    // ...
}
```

#### 3. 添加缓存层

```go
// 使用 Redis 或内存缓存
var peerCache = cache.New(5*time.Minute, 10*time.Minute)

func getPeersCached(networkID string) ([]Peer, error) {
    if peers, found := peerCache.Get(networkID); found {
        return peers.([]Peer), nil
    }
    
    peers, err := getPeersFromDB(networkID)
    if err != nil {
        return nil, err
    }
    
    peerCache.Set(networkID, peers, cache.DefaultExpiration)
    return peers, nil
}
```

#### 4. 增加 HTTP 超时

```nginx
# Nginx 配置（如果使用）
proxy_read_timeout 60s;
proxy_connect_timeout 60s;
proxy_send_timeout 60s;
```

---

### 客户端优化（已实施）

#### 1. 增加 HTTP 超时 ✓

**已修复**: 从 15 秒增加到 30 秒
```go
// internal/client/control.go:83
httpClient := &http.Client{Transport: transport, Timeout: 30 * time.Second}
```

#### 2. 添加重试机制

```go
// 建议添加
func withRetry(fn func() error, maxRetries int) error {
    for i := 0; i < maxRetries; i++ {
        err := fn()
        if err == nil {
            return nil
        }
        if i < maxRetries-1 {
            time.Sleep(time.Duration(i+1) * time.Second)
        }
    }
    return fmt.Errorf("max retries exceeded")
}
```

---

### 架构优化（长期）

#### 1. 部署 CDN 或边缘节点

**方案**:
- 在亚洲部署边缘服务器
- 使用 CDN 加速 API 请求
- 就近访问，减少延迟

**成本**: 中等
**效果**: 减少 50-70% 延迟

#### 2. 实现读写分离

**方案**:
- 写操作 → 主服务器
- 读操作 → 边缘缓存
- 异步同步

**成本**: 中等
**效果**: 提升 5-10x 读性能

#### 3. 数据库优化

**方案**:
- 迁移到 PostgreSQL（如果使用 SQLite）
- 添加索引
- 查询优化
- 连接池

**成本**: 低
**效果**: 提升 10-100x 查询性能

---

## 🔧 诊断脚本

### 服务器端诊断脚本

```bash
#!/bin/bash
# 服务器诊断脚本

echo "=== 系统资源 ==="
top -bn1 | head -20
free -h
df -h

echo ""
echo "=== 网络状态 ==="
netstat -tulpn | grep -E "8090|51820|8091"
ss -tulpn | grep -E "8090|51820|8091"

echo ""
echo "=== 进程状态 ==="
ps aux | grep snet-server | grep -v grep

echo ""
echo "=== 数据库检查 ==="
# SQLite
sqlite3 snet.db "SELECT COUNT(*) FROM nodes;"
sqlite3 snet.db "SELECT COUNT(*) FROM endpoints;"
sqlite3 snet.db "SELECT name FROM sqlite_master WHERE type='index';"

echo ""
echo "=== 日志分析 ==="
tail -100 /var/log/snet-server.log | grep -E "ERROR|WARN|slow"

echo ""
echo "=== 网络延迟测试 ==="
ping -c 5 8.8.8.8
```

### 性能测试脚本

```bash
#!/bin/bash
# 性能测试

echo "=== API 性能测试 ==="
for i in {1..10}; do
  echo "测试 $i:"
  curl -w "响应时间: %{time_total}s\n" -o /dev/null -s \
    "http://localhost:8090/api/v1/networks/W4NSYT6E/peers?relayPorts=1"
done
```

---

## 📈 监控建议

### 1. 添加监控指标

```go
// Prometheus metrics
var (
    apiDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "snet_api_duration_seconds",
            Help: "API request duration",
        },
        []string{"endpoint"},
    )
    
    dbQueryDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "snet_db_query_duration_seconds",
            Help: "Database query duration",
        },
        []string{"query"},
    )
)
```

### 2. 告警规则

```yaml
# Prometheus alert rules
groups:
  - name: snet
    rules:
      - alert: APISlowResponse
        expr: snet_api_duration_seconds > 5
        for: 1m
        annotations:
          summary: "API 响应慢"
          description: "{{ $labels.endpoint }} 响应时间超过 5 秒"
      
      - alert: DBSlowQuery
        expr: snet_db_query_duration_seconds > 2
        for: 1m
        annotations:
          summary: "数据库查询慢"
          description: "{{ $labels.query }} 查询时间超过 2 秒"
```

---

## ✅ 立即行动清单

### 优先级 P0（立即）

1. **检查服务器日志**
   ```bash
   tail -f /var/log/snet-server.log | grep -E "ERROR|WARN|slow"
   ```

2. **检查数据库索引**
   ```bash
   sqlite3 snet.db ".schema" | grep INDEX
   # 如果缺少索引，添加索引
   ```

3. **监控服务器资源**
   ```bash
   htop
   iotop
   ```

### 优先级 P1（24小时内）

1. 优化 peers API 查询
2. 添加缓存层
3. 增加 HTTP 超时配置

### 优先级 P2（1周内）

1. 部署监控和告警
2. 数据库性能调优
3. 考虑部署边缘节点

---

## 📊 预期效果

| 优化项 | 当前 | 预期 | 改善 |
|--------|------|------|------|
| peers API 响应 | 16s | <2s | **-87.5%** |
| 客户端超时失败 | 频繁 | 极少 | **-90%** |
| relayPort 更新 | 失败 | 正常 | **+100%** |
| 连接成功率 | 低 | 高 | **+50-80%** |

---

## 🔗 相关文档

- **服务器代码**: `/cmd/server/snet-server`
- **数据库模型**: `/internal/protocol/types.go`
- **API 实现**: `/internal/server/api.go`
- **客户端优化**: `/internal/client/control.go`

---

**诊断完成！需要我帮你实施具体的优化方案吗？**