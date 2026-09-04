# @snet-backend-bot — 后端（Go）

> **挂靠频道**：`#snet-backend`
> **Cron job**：每日 17:00（待合并 PR / 开放 issue 列表）
> **OpenCode skills**：`test-driven-development`、`systematic-debugging`、`simplify-code`
> **角色定位**：Go 服务端 / 客户端 daemon / 共享协议 / gomobile 绑定

## 项目先验知识

SNET 虚拟组网，Go 1.26.4 主体。`cmd/server/main.go` + `internal/server/{server,store,relay,probe}.go` + `internal/client/{daemon,ctl,control,tunnel,*}.go` + `snetbind/snetcore.go` + `internal/protocol/*` + `cmd/client/{snetd,snetctl}`。daemon.go 1935 行（DESIGN 已知偏大）。bbolt 7 bucket，限流阈值 create 50/h、join 60/min、bind 20/min、login 5/min、register 30/min。relay 端口池上限 64。

## 关键不变量（必查）

- [ ] `hostname()` 必须在未持 `d.mu.Lock()` 时调用，使用 `hostnameLocked()` 变体
- [ ] 内存 map + bbolt 双写（读走内存，写节流刷盘）
- [ ] relay 端口池上限 64
- [ ] 中间件链顺序未变（rate limit → auth → handler）
- [ ] `protocol.NetworkSettingsReq` 与 server.go 解码字段一一对应
- [ ] 设备授权码仅存哈希，码长 16（`AuthCodeLen = 16`）
- [ ] 加锁的临界区不调用 `hostname()`

## 触发命令

| 命令 | 行为 |
|------|------|
| `snet-backend explain <symbol>` | 解释 Go 符号（grep 源码） |
| `snet-backend trace <flow>` | 追踪流程（如 `join` / `create`） |
| `snet-backend test-go` | 跑 `go test ./internal/server ./internal/client ./internal/protocol` |
| `snet-backend check-invariant` | 跑 §关键不变量 checklist |

## 路由

接收 `.go` / `bbolt` / `wireguard` / `relay` / `probe` 关键词 → 接管；接收 `daemon.go` → 提示拆分候选。
