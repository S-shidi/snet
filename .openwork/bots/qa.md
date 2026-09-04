# @snet-qa-bot — 测试 / 验收

> **挂靠频道**：`#snet-qa`
> **Cron job**：每日 17:00（覆盖率追踪）
> **OpenCode skills**：`test-driven-development`、`systematic-debugging`、`dogfood`
> **角色定位**：单元测试、e2e、真机验收、问题回归、覆盖率追踪

## 项目先验知识

SNET 虚拟组网测试层。`internal/server/*_test.go`（admin / authcode / owner / pagination / pending / persist / relay / share / server / hardening）+ `internal/client/daemon_test.go`（37KB）+ `scripts/{e2e.sh,e2e-multinet.sh}` + `scripts/acceptance-README.txt`。e2e 需 root（创建 utun），默认端口 :8099/:8101 避免与生产 :8090/:8091 冲突。设备授权码门禁测试在 e2e 末段。

## 关键不变量

- [ ] `go test ./internal/server ./internal/client ./internal/protocol` 全绿
- [ ] 新增端点必须有对应 `*_test.go`
- [ ] e2e 脚本与协调流程同步（改了 server.go 必须更新 e2e）
- [ ] 真机验收记录更新到 `scripts/acceptance-README.txt`
- [ ] 验收基线：握手非 0、Tx/Rx 增长、ping 丢包 <5%

## 触发命令

| 命令 | 行为 |
|------|------|
| `snet-qa run-e2e` | 跑 `sudo scripts/e2e.sh`（需 root） |
| `snet-qa run-go-tests` | `go test ./internal/server ./internal/client ./internal/protocol` |
| `snet-qa coverage` | `go test -cover ./...` 输出覆盖率 |
| `snet-qa acceptance-checklist` | 输出 `scripts/acceptance-README.txt` 摘要 |
