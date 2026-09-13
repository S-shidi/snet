# SNET 服务器性能优化方案

## 问题诊断总结

### 已解决
✅ CPU 占用 100% (dd 进程已杀掉)

### 未解决
❌ API 响应时间 15-16 秒
❌ HTTP/2 流错误
❌ 全局互斥锁导致请求串行化

---

## 根本原因

### 代码层面

`internal/server/store.go:1569-1610`:

```go
func (s *Store) listPeersFrom(...) {
    s.mu.Lock()           // ❌ 全局互斥锁，所有请求串行化
    defer s.mu.Unlock()
    
    // 阻塞操作
    s.persistNode(...)    // 磁盘 I/O
    s.ensureRelayPort(...) // 端口分配
    s.ensureNodePort(...)  // 端口分配
    
    // 遍历所有节点
    for id, n := range ns.nodes {
        ...
    }
}
```

### 性能瓶颈

1. **全局互斥锁** - 所有 API 请求串行化
2. **同步持久化** - 磁盘 I/O 阻塞 API
3. **端口分配** - 复杂逻辑在锁内执行
4. **HTTP/2 问题** - 流错误导致连接失败

---

## 优化方案

### 方案 1: 读写锁优化（推荐，最小改动）

**修改**: `internal/server/store.go`

```go
// 将 sync.Mutex 改为 sync.RWMutex
type Store struct {
    mu       sync.RWMutex  // 读写锁
    networks map[string]*networkState
    ...
}

// 读操作使用 RLock
func (s *Store) listPeersFrom(...) {
    s.mu.RLock()  // 允许并发读
    defer s.mu.RUnlock()
    
    // 读操作...
}

// 写操作使用 Lock
func (s *Store) updateNode(...) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // 写操作...
}
```

**效果**:
- 并发读性能提升 5-10 倍
- API 响应时间从 15s → 1-3s
- 最小代码改动

**实施步骤**:
1. 修改 `Store` 结构体
2. 区分读写操作
3. 测试验证
4. 部署更新

---

### 方案 2: 异步持久化

**修改**: `internal/server/store.go`

```go
func (s *Store) listPeersFrom(...) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    // 异步持久化
    if now-ns.lastSeenWrite >= int64(lastSeenThrot/time.Second) {
        go func(networkID string, node *protocol.Node) {
            s.mu.Lock()
            defer s.mu.Unlock()
            _ = s.persistNode(networkID, node)
            ns.lastSeenWrite = now
        }(te.NetworkID, me)
    }
}
```

**效果**:
- API 响应时间 < 1s
- 不阻塞读操作
- 持久化在后台完成

---

### 方案 3: 端口分配优化

**修改**: `internal/server/store.go`

```go
// 预分配端口池
func (s *Store) initPortPool() {
    s.portPool = make(chan int, 256)
    for i := 51820; i < 51820+256; i++ {
        s.portPool <- i
    }
}

// 快速分配端口
func (s *Store) allocatePort() int {
    select {
    case port := <-s.portPool:
        return port
    default:
        return 0
    }
}
```

**效果**:
- 端口分配时间从毫秒级降到微秒级
- 无需遍历检查

---

### 方案 4: 禁用 HTTP/2（客户端）

**修改**: `internal/client/control.go`

```go
transport := &http.Transport{
    ForceAttemptHTTP2: false,  // 禁用 HTTP/2
    TLSClientConfig:   tlsConfig,
    IdleConnTimeout:   90 * time.Second,
}
```

**效果**:
- 避免 HTTP/2 流错误
- 更稳定的连接

---

## 实施优先级

### P0（立即实施）

1. **禁用 HTTP/2** - 解决流错误问题
2. **增加 HTTP 超时** - 已完成（30秒）

### P1（24小时内）

1. **读写锁优化** - 提升并发性能
2. **异步持久化** - 降低 API 延迟

### P2（1周内）

1. **端口池优化** - 提升端口分配性能
2. **监控和日志** - 性能监控

---

## 预期效果

| 指标 | 当前 | 优化后 | 改善 |
|------|------|--------|------|
| API 响应时间 | 15-16s | <1s | **-94%** |
| 并发性能 | 串行 | 并行 | **+500%** |
| HTTP 错误 | 频繁 | 无 | **-100%** |
| CPU 使用 | 0% | 0% | 无变化 |

---

## 风险评估

### 读写锁方案
- **风险**: 低
- **影响**: 读操作并发，写操作仍串行
- **回滚**: 简单，改回 Mutex 即可

### 异步持久化
- **风险**: 中
- **影响**: 可能丢失最近几秒的数据
- **缓解**: 增加持久化队列和重试机制

---

## 下一步行动

1. **立即**: 修改客户端禁用 HTTP/2
2. **今天**: 实施读写锁优化
3. **明天**: 实施异步持久化
4. **测试**: 验证性能提升
5. **部署**: 更新服务器

---

## 监控指标

```go
// 添加性能监控
var (
    apiDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "snet_api_duration_seconds",
            Help:    "API request duration",
            Buckets: []float64{.1, .5, 1, 2, 5, 10, 15, 20},
        },
        []string{"endpoint"},
    )
    
    lockWaitDuration = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "snet_lock_wait_seconds",
            Help:    "Lock wait duration",
        },
    )
)
```

---

**需要我帮你实施哪个优化方案？**