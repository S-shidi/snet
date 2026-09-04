# SNET 研发团队 BOT + 群组规划方案

> **项目**：SNET 虚拟组网（`/Users/shidi/OpenWork/Snet`）
> **生成日期**：2026-09-04
> **生成者**：Hermes Agent（基于 README.md / DESIGN.md / 全量源码深度阅读）
> **目标平台**：用户自选（推荐 Slack，`.opencode/commands/slack-plugin/` 已有 5 个成熟模板）

---

## 0. 项目技术画像（给每个 BOT 的"先验知识"）

通读源码后提炼出每个 BOT 必须知道的关键事实，作为 prompt 注入：

| 维度 | 关键事实 |
|------|---------|
| **语言栈** | Go 1.26.4（服务端 + daemon + CLI + gomobile 绑定）、Rust 2021（Tauri v2 桌面端代理层）、Kotlin 1.9.22（Android 壳）、TypeScript 5（共享 UI） |
| **核心依赖** | `go.etcd.io/bbolt`（持久化，7 个 bucket）、`golang.zx2c4.com/wireguard`、`golang.org/x/mobile`（gomobile） |
| **数据面** | WireGuard UDP 隧道，优先 NAT 打洞（每 15s 探测 :8091），失败回落 UDP 中继（51820-51883） |
| **控制面** | 协调面：HTTPS REST JSON :8090；本地控制面：HTTP :19432（loopback） |
| **存储** | bbolt 7 bucket：`networks / nodes / tokens / devices / pending / admin / authcodes` |
| **客户端** | Desktop（Tauri v2 + Rust 22 命令代理）/ Android（WebView + Kotlin 桥 + gomobile AAR）/ Docker（snetd + nginx） |
| **共享 UI** | `shared/web/{ui,types,utils,subnet}.ts` + `index.html` + `styles.css`，由 `scripts/build-web.sh` 同步到 Android 与 Docker |
| **协议标识** | `snet://join?nid=xxx&code=yyy[&name=xxx][&server=xxx]`（`internal/protocol/link.go`） |
| **关键端口** | 8090 协调 / 8091 探测 / 51820-51883 中继 / 51900+ Docker 客户端 / 19432 本地控制 |
| **当前阶段** | DESIGN.md §10 描述的"社区共享 / 角色模型 / Visibility"是主要演进方向；DESIGN 已知局限：无集群、本地 /ctl 无认证、relay 端口上限 64、daemon.go 1935 行偏大 |
| **测试基线** | `go test ./internal/server ./internal/client ./internal/protocol` + `scripts/e2e.sh`（需 root）+ 验收见 `scripts/acceptance-README.txt` |
| **CI** | `.github/workflows/build-windows.yml`（NSIS 安装包） |
| **部署** | VPS 66.187.6.46（`scripts/rollout-vps.sh`）、Docker（`deploy/docker/`）、macOS launchd、Windows SCM |
| **绑定域** | 生产：`https://snet.uizhi.eu.org:8090`，CA 固定 |
| **目录结构入口** | `cmd/{server,client/snetd,client/snetctl}` + `internal/{server,client,protocol}` + `desktop/src-tauri` + `android/app` + `snetbind` + `shared/web` + `deploy/` + `scripts/` |

---

## 1. 团队架构总览

```
                    ┌─────────────────────────────┐
                    │  #snet-pm  (PM 协调 / 总览)  │
                    │  @snet-pm-bot               │
                    └──────────┬──────────────────┘
                               │ 同步 / 跨组广播
       ┌───────────┬───────────┼───────────┬───────────┬───────────┐
       ▼           ▼           ▼           ▼           ▼           ▼
  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐
  │ 架构组  │ │ 后端组  │ │ 前端组  │ │ 移动端组 │ │ DevOps  │ │   QA    │
  │#snet-   │ │#snet-   │ │#snet-   │ │#snet-   │ │#snet-   │ │#snet-   │
  │ arch    │ │ backend │ │ frontend│ │ mobile  │ │ devops  │ │ qa      │
  │@arch-   │ │@backend-│ │@fe-     │ │@mobile- │ │@devops- │ │@qa-     │
  │ bot     │ │ bot     │ │ bot     │ │ bot     │ │ bot     │ │ bot     │
  └─────────┘ └─────────┘ └─────────┘ └─────────┘ └─────────┘ └─────────┘
       │           │           │           │           │           │
       └───────────┴───────────┴───────────┴───────────┴───────────┘
                          ↓ 收口到 #snet-pm
```

**规模**：1 个 PM 群 + 6 个职能群 = **7 个 Slack 频道**，**7 个 OpenCode BOT**（每个频道一个）。

---

## 2. 群组（频道）清单

> 命名约定：`#snet-<role>` 小写连字符；私有频道（除 PM 群对外可保留 read-only 公共频道 `#snet-announce` 用于发版通告，但本方案不强求）。

| # | 频道 | 可见性 | 用途 | 关键订阅 |
|---|------|--------|------|---------|
| 1 | `#snet-pm` | 私有 | 跨组协调、周会、里程碑追踪、PR 摘要广播 | 所有 BOT 周报自动聚合到此 |
| 2 | `#snet-arch` | 私有 | 协议演进、DESIGN.md 维护、API 评审、跨切面决策 | arch-bot |
| 3 | `#snet-backend` | 私有 | Go 服务端 / daemon / gomobile 绑定层 | backend-bot |
| 4 | `#snet-frontend` | 私有 | Tauri 桌面代理 / 共享 Web UI / 平台适配器 | frontend-bot |
| 5 | `#snet-mobile` | 私有 | Android（Kotlin + WebView + gomobile AAR）| mobile-bot |
| 6 | `#snet-devops` | 私有 | VPS 部署、Docker、CI、Windows 安装包、systemd/launchd/SCM | devops-bot |
| 7 | `#snet-qa` | 私有 | e2e、单元/集成测试、真机验收、问题回归 | qa-bot |

**推荐附加频道**（可选，不计入主结构）：
- `#snet-announce`（公开只读）→ 仅 devops-bot 有写权限，发布 release notes 与 VPS 维护窗口。
- `#snet-incidents`（私有）→ QA / DevOps 在严重事件时把 root-bot 拉进来。

---

## 3. BOT 详细规格

> 每个 BOT 都是 **OpenCode agent + 群组频道路由 + 项目专属 SKILL.md** 的组合。
> prompt 注入的"项目先验知识"统一从 `TEAM_PLAN.md §0` 复制（避免每个 BOT 重复造轮子）。
> 推荐使用 `.opencode/commands/slack-plugin/` 已有的 5 个命令作为基础工作流。

### 3.1 `@snet-pm-bot` — 团队协调

| 字段 | 值 |
|------|-----|
| **挂靠频道** | `#snet-pm` |
| **核心职责** | 周会主持、跨组议题汇总、PR 摘要广播、里程碑追踪 |
| **触发命令** | `/standup`、`/channel-digest`、`/draft-announcement` |
| **必备技能** | slack-plugin：`slack-search`、`slack-messaging`、`slack-cli` |
| **OpenCode skills** | `requesting-code-review`、`writing-plans` |
| **不允许** | 直接动代码、修改 DESIGN.md |
| **每日定时** | 09:00 拉取所有 6 个职能频道昨日消息 → 在 #snet-pm 输出"昨日要闻"摘要 |
| **每周定时** | 周一 10:00 汇总上周 git log → 输出"上周合并 / 待合 / 讨论中"三栏表 |
| **路由规则** | 接收 `@snet-pm-bot help` → 输出可用命令；接收 `@snet-pm-bot broadcast <text>` → 同步转发到全部 6 个职能频道 |

**BOT prompt 注入片段**：
```
你是 SNET 团队的 PM 协调 BOT，托管频道 #snet-pm。
- 你不是开发者，不要修改代码
- 你的工作语言：中文（用户语言）
- 你的产出：摘要、纪要、待办清单、里程碑状态
- 当被问"现在 SNET 进展如何"时，先读 DESIGN.md §10（社区共享路线图）+ 最近 7 天 git log
```

---

### 3.2 `@snet-arch-bot` — 架构 / 协议

| 字段 | 值 |
|------|-----|
| **挂靠频道** | `#snet-arch` |
| **核心职责** | 协议演进、API 设计评审、DESIGN.md 维护、跨切面决策 |
| **重点文件** | `DESIGN.md`、`internal/protocol/{types,link}.go`、`internal/server/server.go`（HTTP handler） |
| **触发命令** | `/summarize-channel`、`/find-discussions <keyword>`、`@snet-arch-bot review-api <diff>` |
| **必备技能** | `requesting-code-review`（架构评审模式）、`plan` |
| **每周输出** | 扫描 `internal/protocol/types.go` 与 DESIGN.md 的不一致 → 在频道发出"协议/文档漂移告警" |
| **路由规则** | 接收 `protocol:` / `api:` / `schema:` 前缀 → 自动 ack 并 @当前负责人 |

**专属 trigger phrases**：
- `protocol review` → 进入协议评审模式
- `api endpoint` → 检查 `server.go` 路由表（~30 端点）
- `community share` → 拉取 DESIGN.md §10 上下文

---

### 3.3 `@snet-backend-bot` — 后端（Go）

| 字段 | 值 |
|------|-----|
| **挂靠频道** | `#snet-backend` |
| **核心职责** | Go 服务端 / 客户端 daemon / 共享协议实现 / gomobile 绑定 |
| **重点文件** | `cmd/server/main.go`、`internal/server/*`（~1178+3288+150KB）、`internal/client/*`（daemon.go 1935 行）、`internal/protocol/*`、`snetbind/snetcore.go`（22 导出方法）、`cmd/client/{snetd,snetctl}` |
| **触发命令** | `@snet-backend-bot explain <symbol>`、`@snet-backend-bot trace <flow>`、`@snet-backend-bot test-go` |
| **必备技能** | `test-driven-development`、`systematic-debugging`、`simplify-code`（daemon.go 拆分候选） |
| **路由规则** | 接收 `.go` / `bbolt` / `wireguard` / `relay` / `probe` 关键词 → 自动接管；接收 `daemon.go` → 提示"已知偏大（1935 行），建议按子职责拆分" |
| **关键不变量（必须背诵）** | - `hostname()` 必须在未持 `d.mu.Lock()` 时调用，使用 `hostnameLocked()` 变体<br>- 内存 map + bbolt 双写（读走内存，写节流刷盘）<br>- relay 端口池上限 64<br>- 限流：create 50/h、join 60/min、bind 20/min<br>- 设备授权码仅存哈希，码长 16 |

**专属 checklist**（每次改 server.go 自动跑）：
- [ ] 中间件链顺序未变（rate limit → auth → handler）
- [ ] `protocol.NetworkSettingsReq` 与 `server.go` 解码字段一一对应
- [ ] bbolt bucket 7 个，未新增/删除
- [ ] 加锁的临界区不调用 `hostname()`

---

### 3.4 `@snet-frontend-bot` — 前端（Tauri 桌面 + 共享 Web UI）

| 字段 | 值 |
|------|-----|
| **挂靠频道** | `#snet-frontend` |
| **核心职责** | Tauri 桌面代理层、共享 TypeScript UI、三平台适配器、样式 |
| **重点文件** | `desktop/src-tauri/src/{lib.rs,snet.rs,main.rs}`、`desktop/src/{desktop.ts,web.ts,main.ts,styles.css}`、`shared/web/{ui.ts,types.ts,utils.ts,subnet.ts,index.html,styles.css}` |
| **触发命令** | `@snet-frontend-bot sync-web`、`@snet-frontend-bot check-adapters` |
| **必备技能** | `subagent-driven-development`（三适配器一致性强校验） |
| **路由规则** | 接收 `ts` / `tauri` / `vite` / `esbuild` / `adapter` 关键词 → 接管；接收 `Backend` 接口 → 自动 diff `shared/web/types.ts` 与三个 adapter 实现是否一致 |
| **构建联动** | 接收 `sync-web` → 执行 `scripts/build-web.sh` 并把产物路径贴在频道 |

**专属强校验**（每次 PR 触发）：
- [ ] `shared/web/types.ts` 的 `Backend` 接口 19 方法，三个 adapter（`desktop.ts` / `web.ts` / `android.ts`）必须全实现
- [ ] 修改 `shared/web/ui.ts` 后是否已跑 `scripts/build-web.sh`（commit message 提示）
- [ ] `tsconfig.json` target 与 esbuild target 一致（当前 es2022）

---

### 3.5 `@snet-mobile-bot` — 移动端（Android）

| 字段 | 值 |
|------|-----|
| **挂靠频道** | `#snet-mobile` |
| **核心职责** | Android 端：Kotlin 桥 / VPN Service / WebView / gomobile AAR 构建 |
| **重点文件** | `android/app/src/main/kotlin/com/snet/app/{MainActivity,SnetBridge,SnetVpnService,WebBridge,HardwareID,BootReceiver}.kt`、`android/src/android.ts`、`android/build-android.sh`、`snetbind/snetcore.go` |
| **触发命令** | `@snet-mobile-bot build-aar`、`@snet-mobile-bot check-bridge` |
| **必备技能** | `requesting-code-review`（移动端模式）、`systematic-debugging` |
| **路由规则** | 接收 `kotlin` / `android` / `vpn` / `webview` / `gomobile` / `aar` → 接管 |
| **关键不变量** | - VPN 路由只隧道 `10.0.0.0/8`（非全局）<br>- VPN Builder 必须排除协调服务器 IP（防路由环路）<br>- `HaltTunnels()` 保留 daemon 状态，VPN 重建后瞬间恢复<br>- `@JavascriptInterface` 重操作（join/leave/remove）必须在单线程 executor 串行化<br>- 设备 ID 派生：`Build.* + ANDROID_ID → SHA-256 → 16 hex` |

**专属 checklist**（改 Kotlin 文件时跑）：
- [ ] 设备 ID 派生是否仍是硬件绑定（避免文件持久化）
- [ ] VPN Service 是否处理了 `tunFd` 尚未就绪的 `pendingStart` 路径
- [ ] WebBridge 的 `@JavascriptInterface` 方法是否在后台线程执行
- [ ] `BootReceiver` 注册的 `BOOT_COMPLETED` 权限未漂移

---

### 3.6 `@snet-devops-bot` — 部署 / 运维

| 字段 | 值 |
|------|-----|
| **挂靠频道** | `#snet-devops` |
| **核心职责** | VPS 部署、Docker 镜像、CI、Windows 安装包、systemd/launchd/SCM 单元 |
| **重点文件** | `scripts/{rollout-vps,rollout-mac,build-web,e2e,e2e-multinet,remote-join,mac-daemon-migrate}.sh`、`deploy/{snet-server.service,phone-android.conf,docker/*}`、`.github/workflows/build-windows.yml`、`Dockerfile`（root + desktop 镜像） |
| **触发命令** | `@snet-devops-bot rollout-vps`、`@snet-devops-bot check-ports` |
| **必备技能** | `simplify-code`（部署脚本）、`test-driven-development`（e2e 套件） |
| **路由规则** | 接收 `vps` / `docker` / `ci` / `windows` / `nsis` / `systemd` / `launchd` / `scm` → 接管；接收 `rollout` → 必提示当前生产 host (`root@66.187.6.46`) 与凭证来源 (`SNET_ADMIN_USER` / `SNET_ADMIN_PASSWORD`) |
| **关键不变量** | - 服务端 :8090/:8091/51820-51883 已被占用 → 部署前必须扫描<br>- Docker 客户端必须用 `--network host` + `--cap-add NET_ADMIN` + `--device /dev/net/tun`<br>- 服务端与客户端 relay 端口不能重叠（51820-51883 服务端，51900+ Docker 客户端）<br>- 升级时用 `.upload-<bin>` 临时名 + `mv` 原子替换（防止运行中文件被覆盖） |

**专属强校验**（部署前自动跑）：
- [ ] `scripts/rollout-vps.sh` 检查 `$BIN/{server,snetd,snetctl}` 已构建
- [ ] `env SNET_REQUIRE_DEVICE_AUTH=1` 是否纳入 `/etc/snet-server.env`
- [ ] systemd unit 文件 hash 与上次部署一致（避免被篡改）

---

### 3.7 `@snet-qa-bot` — 测试 / 验收

| 字段 | 值 |
|------|-----|
| **挂靠频道** | `#snet-qa` |
| **核心职责** | 单元测试、e2e、真机验收、问题回归、覆盖率追踪 |
| **重点文件** | `internal/server/*_test.go`（admin / authcode / owner / pagination / pending / persist / relay / share / server / hardening）、`internal/client/daemon_test.go`（37KB）、`scripts/{e2e.sh,e2e-multinet.sh}`、`scripts/acceptance-README.txt` |
| **触发命令** | `@snet-qa-bot run-e2e`、`@snet-qa-bot coverage` |
| **必备技能** | `test-driven-development`、`systematic-debugging`、`dogfood` |
| **路由规则** | 接收 `test` / `e2e` / `coverage` / `regression` / `acceptance` → 接管；接收 `LastHandshakeSec` → 立刻拉真机验收模板 |
| **关键不变量** | - e2e 需 root（创建 utun）<br>- e2e 默认端口 :8099/:8101，避免与生产 :8090/:8091 冲突<br>- 设备授权码门禁测试在 e2e 末段（`require-device-auth` 路径）<br>- 验收基线：Mac ↔ 手机，中继 51821 端口，握手非 0、Tx/Rx 增长 |

**专属 checklist**（每个 PR 自动跑）：
- [ ] `go test ./internal/server ./internal/client ./internal/protocol` 全绿
- [ ] 新增端点是否有对应 `*_test.go`
- [ ] e2e 脚本是否被同步修改（如果改了协调流程）
- [ ] 真机验收记录是否更新到 `scripts/acceptance-README.txt`

---

## 4. 跨组路由 / 触发矩阵

> 哪些群组的哪些关键词会触发跨组协作（自动 @ 对应 BOT）

| 触发内容 | 主路由 | 抄送 |
|---------|--------|------|
| 改 `DESIGN.md` | `#snet-arch` | `#snet-pm` |
| 改 `internal/protocol/*.go` | `#snet-arch` + `#snet-backend` | — |
| 改 `internal/server/*` | `#snet-backend` | `#snet-qa`（触发测试） |
| 改 `internal/client/*` | `#snet-backend` | `#snet-mobile`（若改 tunnel_*.go）、`#snet-qa` |
| 改 `snetbind/*` | `#snet-mobile` + `#snet-backend` | — |
| 改 `desktop/src*` | `#snet-frontend` | — |
| 改 `shared/web/*` | `#snet-frontend` | `#snet-mobile`（Android 同步） |
| 改 `android/**` | `#snet-mobile` | `#snet-frontend`（android.ts） |
| 改 `deploy/**` 或 `scripts/rollout*` | `#snet-devops` | `#snet-pm`（发版通告） |
| 改 `.github/workflows/**` | `#snet-devops` | — |
| 改 `**/*_test.go` 或 `scripts/e2e*` | `#snet-qa` | 改动对应职能组 |
| Release tag `v*` | `#snet-pm` 自动汇总 | 全频道 |

**实现机制**：每个 BOT 的 `git diff --name-only` 监听钩子，匹配上表后自动 ack + 路由。

---

## 5. 落地步骤（用户手动执行）

### 5.1 创建 Slack 频道（7 个）
```
#snet-pm, #snet-arch, #snet-backend, #snet-frontend, #snet-mobile, #snet-devops, #snet-qa
```
建议：私有频道，统一前缀 `snet-`，归档策略 90 天。

### 5.2 为每个频道创建 BOT
1. 在 Slack App 管理后台为每个 BOT 创建独立 App（命名：`snet-pm-bot` 等）。
2. 赋予 `chat:write` / `channels:history` / `commands` / `users:read` 权限。
3. 用 `slack-cli`（`~/.slack/bin/slack`）安装到对应 workspace：
   ```bash
   slack install --team <workspace>
   ```
4. 复制 `.opencode/commands/slack-plugin/{standup,channel-digest,summarize-channel,find-discussions,draft-announcement}.md` 到每个 BOT 的命令目录。

### 5.3 给每个 BOT 注入 SKILL.md
每个 BOT 的 OpenCode skill 目录放 `SKILL.md`，内容包含：
- §0 项目先验知识
- §3 对应 BOT 的规格表
- §4 路由矩阵（仅本 BOT 视角）

推荐路径（参考现有 slack-plugin 结构）：
```
.opencode/skills/snet-<role>/SKILL.md
```

### 5.4 配置定时任务
- pm-bot：cron `0 9 * * *` 拉昨日摘要；`0 10 * * 1` 拉周报
- 6 个职能 BOT：cron `0 17 * * *` 输出当日"待合并 PR / 开放 issue"列表

### 5.5 接入 git hooks
在 `.git/hooks/post-commit` 写入 `git diff --name-only HEAD~1` → 匹配 §4 路由表 → 调对应 BOT 的 slack 通知。

---

## 6. 推荐使用 `.opencode/commands/slack-plugin/` 的命令（已存在）

直接复用，不需要新写：

| 命令 | 用于哪个 BOT | 触发场景 |
|------|-------------|---------|
| `standup.md` | pm-bot | 周一自动汇总个人/团队 standup |
| `channel-digest.md` | pm-bot | 每日 09:00 拉所有频道昨日消息 |
| `summarize-channel.md` | arch-bot / pm-bot | 周会前快速回顾某频道 |
| `find-discussions.md` | arch-bot | 按关键词 `protocol` / `api` / `community` 反查历史讨论 |
| `draft-announcement.md` | devops-bot | 发版通告草稿生成 |

---

## 7. 风险与边界

- **Bolt 数量**：7 个 BOT 已接近 Slack 单 workspace 推荐上限（10）。新增 BOT 前先评估能否复用。
- **避免循环广播**：pm-bot 广播时必须带 `[broadcast]` 前缀，接收方 BOT 收到后不二次转发。
- **凭据隔离**：devops-bot 的 VPS 凭据走 `~/.slack/secrets/` 而非 prompt 注入；其余 BOT 禁止接触生产凭据。
- **OpenCode 配额**：7 个 BOT 并发跑 `git log` / `find` 可能占满上下文窗口，pm-bot 应做去重聚合。
- **协议版本**：当前协议无版本号（DESIGN 已知局限 #3），任何改 `internal/protocol/types.go` 的 PR 必须在 #snet-arch 强制评审。

---

## 8. 一句话总结

> **7 个频道 × 7 个 BOT** = 1 个 PM 协调群 + 6 个职能群（架构/后端/前端/移动端/DevOps/QA）。
> 每个 BOT 都是 `slack-plugin 命令 + 项目专属 SKILL.md + §0 项目先验知识` 三件套。
> 路由靠 `git diff --name-only` 钩子驱动，关键不变量由各 BOT 自己的 checklist 守护。
