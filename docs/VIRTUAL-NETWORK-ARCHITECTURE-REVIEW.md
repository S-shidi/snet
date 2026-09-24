# SNET 虚拟组网架构评审报告

**评审时间**: 2026-09-24  
**联调对象**: 诗迪网络 (W4NSYT6E, 10.88.1.0/24)  
**参与节点**: Mac (10.88.1.3) / VPS Docker (10.88.1.2) / Android 手机 (10.88.1.6) / 群晖 NAS (10.88.1.4, 离线)  
**会话背景**: 本轮完成手机端 Android 原生客户端成员列表修复、三端互通验证与调试日志清理，三方提交已推送。

---

## 一、本次联调结果总结

### 1.1 实测连通性

| 方向 | 丢包率 | 延迟 (RTT) | 路径 |
|------|--------|-----------|------|
| Mac → 手机 | **0%** (4/4) | ~655ms | relay (CGNAT) |
| Mac → VPS | **0%** (3/3) | ~360ms | relay |
| VPS → 手机 | 50% (2/4) | ~355ms | relay (CGNAT 多出口竞争) |
| 手机 WG 隧道 | Mac/VPS **online**, handshake 持续 | rx/tx 持续增长 | relay 单播 |

### 1.2 关键发现

- **成员列表根因**: Android 原生客户端统一走 owner-only 的 `GET /networks/{nid}`(`info()`)，手机是普通成员收到 `401` → 列表恒空。修复后按角色分发：owner 走 `info()`，member 走 `/peers`(member-scoped)。
- **CGNAT 多出口轮换**是数据面 50% 丢包的主因：手机活动端口在 `223.160.208.29` / `223.160.209.29` 间轮换，回程帧可能沿旧映射发送。
- **relay 单播路由健康**:VPS 部署带诊断日志版本后确认所有中继帧 `UNICAST ok=true`，控制面探测与数据面流分离正确，`ctrlOnly` 排除有效。
- **三端重启后自动恢复**:清理日志重新部署 VPS 后，三端隧道自动重建，且 Mac↔VPS 从 33% 丢包改善到 0%。

---

## 二、虚拟组网架构方案解析

### 2.1 控制面架构

```
                    ┌─────────────────────────────┐
                    │      VPS (66.187.6.46)      │
                    │  HTTPS :8090 (SNET server)  │
                    │  SQLite (node/device/auth)  │
                    │  admin console / REST API   │
                    └─────────────────────────────┘
                          ▲      ▲      ▲      ▲
                netinfo  / │ peers│ code │join │
                         /  │      │      │      │
              ┌────────┐   ┌───┐ ┌───┐ ┌───┐
              │  Mac   │   │VPS│ │PH │ │NAS│
              └────────┘   └───┘ └───┘ └───┘
```

- 保存节点、设备绑定、认证码、网络拓扑于服务端 SQLite。
- 控制面基于 HTTPS **Bearer token** 认证，token 按网络作用域。
- 权限分层：owner(全权) / admin(管理) / member(成员，仅 `/peers` 等只读查询)。
- 加入机制：邀请链接 (`snet://join?nid=&code=`) + 配对码，支持 pending→approve/deny 审批流。
- 设备认证 (`绑定`)、授权过期检查已实现。

### 2.2 数据面架构(relay 中继)

```
                 VPS relay (66.187.6.46)
       51820 广播/控制  51821  51822  51823   ... 51820+255
          │             │      │      │   
          │   unicast frame  │      │
        ┌─┴─┐  ┌─────┐  ┌───┐  ┌───┐
        │relay│ │ VPS │  │ PH │  │MAC│
        └────┘└─────┘  └───┘  └───┘
         WG peer ←──── 皆经 relay 转发
```

- WireGuard 隧道封装，但寻址经 **relay 单播端口池**：每节点分发唯一 relay 端口 (51821+)，控制面广播端口 51820。
- 帧路由：`RelayRouteNodePort` 基于发送方 CG 地址归因 → 目标节点的 inbound 映射 → `UNICAST` 单播转发。
- 兜底：流未建立时 `SendFanned` 从广播端口 fan-out 到所有活跃对端，保证 handshake init 不丢。
- 打洞：`whoami`/`group` 控制协议在带内完成 NAT 映射发现，`ctrlOnly` 排除临时探测 socket。

### 2.3 逻辑分层

```
┌───────────────────────────────────────────────┐
│ 应用层: 成员管理 / 审批 / 邀请 / 设备 / 路由  │
├───────────────────────────────────────────────┤
│ 控制面: HTTPS REST (auth, netinfo, peers...)   │
├───────────────────────────────────────────────┤
│ 数据面: WireGuard UDP (加密隧道)              │
│   ├─ relay 单播 (默认路径)                    │
│   └─ NAT 打洞直连 (已实现，未成为常态)        │
├───────────────────────────────────────────────┤
│ Base: 中继端口池 lazy-bind + 单播路由 + 扇出   │
└───────────────────────────────────────────────┘
```

---

## 三、可行性评估

### 3.1 结论:方案**可行**

- **功能完备**:创建/加入/审批/退出/删除/踢出/管理后台全链路已闭环。
- **跨 NAT 可用**:CGNAT 后设备(手机)能建立隧道并收发数据，relay 单播+扇出兜底能保证握手必达。
- **多端互通实测**:0% 丢包路径存在 (Mac↔手机/ Mac↔VPS)，满足基本组网需求。
- **权限正确**:成员只读 `/peers`，owner 才可 `info()`——符合最小权限。
- **自部署**:VPS 上单二进制 + SQLite + 系统服务，运维轻量。

### 3.2 可行性边界条件

| 条件 | 现状 | 说明 |
|------|------|------|
| VPS 必须在线 | ✅ 依赖 | 中继与协调都依赖 VPS |
| 公网可达中继端口 | ✅ | 51820-52075 UDP 池 |
| 运营商允许 UDP | ⚠️ CGNAT | 手机经运营商 CGNAT,UDP 高损时段可能不稳 |
| 手机常驻 | ⚠️ 电池/进程 | 息屏/后台回收风险 |
| 单中继带宽上限 | ⚠️ | 数据面全走 VPS,受限于其带宽 |

---

## 四、优缺点分析

### 4.1 优点

1. **架构简单、自包含**
   - 单服务端 + SQLite + REST,无外部依赖(KV/db/协调器)。
   - 部署运维成本低：一条 systemd 服务,一个二进制。

2. **权限与安全模型清晰**
   - token 按网络作用域,owner/admin/member 三级。
   - 邀请码+审批流控制加入,设备绑定做设备级认证。
   - 数据面 WireGuard 内建加密,relay 仅转发不解密。

3. **relay 单播路由设计巧妙**
   - 端口池 + UNICAST 精准转发,避免广播放大(仅兜底时扇出)。
   - `ctrlOnly` 排除探测 socket,杜绝控制面流毒化数据面。
   - CG 多宿主反向索引 (`nodeByHost`) 容忍 NAT 多出口。

4. **兜底机制保障健壮性**
   - `SendFanned` 保证握手前无流也能触达。
   - 邻居 /23-/16 归属作为最终兜底减少首包丢弃。
   - `lastSeenTTL` 判定在线,`zombie-ttl` 清理僵尸。

5. **跨平台生态完整**
   - 服务端 + Linux daemon + Mac + Android(原生 Compose)+ 网页 admin,一套协议通吃。

### 4.2 缺点

1. **控制面/数据面强耦合于单一路由 VPS**
   - 所有数据帧无论远近都必须经过 VPS relay → 单点故障、带宽瓶颈、延迟放大。
   - 实测 Mac↔手机 RTT ~655ms(与 VPS 同区域才 360ms),延时主要由"绕远中继"贡献。

2. **NAT 打洞未成为常态路径**
   - 虽然协议支持直连(打洞/o/6 直连、端点发现),但实际所有流量仍走 relay。
   - 直连失败率高(尤其 CGNAT),又没有"失败后回退+定期再试"的主动性 → 中继长期兜底。

3. **CGNAT 多出口导致的回程不稳定**
   - 手机在 208.29 / 209.29 间轮换,回程帧可能发到旧映射 → 50% 丢包。
   - 无多路径扇出容错(只单播到最新 flow),竞争窗口丢帧。

4. **移动端保活与功耗矛盾**
   - 数据面 keepalive + 2s 控制面轮询,前台可用但后台耗电/被回收。
   - 息屏后 WG 会话可能冻结,恢复感知依赖下一次探测。

5. **可观测性不足**
   - 有 admin console 与质量监控(quality.go),但缺统一的端到端 latency/loss 矩阵与历史曲线。
   - 无中继带宽/队列水位监控,无告警通道。

6. **无自动故障转移**
   - VPS 单机无 HA;备份节点切换需人工。
   - 无多 region 中继、无智能选优。

---

## 五、改进计划

### 5.1 P0:解决 CGNAT 回程不稳(缓解丢包)

**目标**:手机这种多出口 CGNAT 节点,当前 50% 丢包降低到数据面抖动可控。

- **多地址扇出**(服务端):对连接 `nodeByHost` 有多个地址的节点,允许 sender port 上对**其全部 host** 的活跃 flow 都发一份副本(广播兜底常开,不再只 UNICAST 最新映射)。代价是流量放大,但对 2-4 个出接口场景可接受。
- **短 TTL 收敛**:`relaySeen` 过期时间从较长值缩短(如 30s),让陈旧出口映射更快退出集合,减少回程发到死地址。
- **探测优先级**:手机侧从随机出接口改绑单出口(若运营商允许)或至少让 WG 通过同源 socket 与 server 保持联系,稳定 `ctrlHost`。

### 5.2 P1:降低对单 relay 的依赖(架构演进)

1. **NAT 打洞主动化**
   - 直连地址(端到端候选)优先用,失败后回退 relay;并在流空闲 N 秒后**周期性重启打洞尝试**。
   - 客户端每次 `/peers` 已携带直连候选/`relayFlow`;打洞逻辑已存在,只缺"成功后切换数据面 + 健康度看门狗"。
2. **多中继选优**
   - 支持多个 relay 节点(不同 region),客户端按实测延迟/丢包选最优。
   - 中继间互不集群,仅共享同一协调协议;数据面在客户端切换。
   - 最小实现:在 `/peers` 返回多候选 `relayEndpoint[]`,客户端选延迟最低。
3. **HA 化 VPS**(可选)
   - 双 VPS + keepalived 浮动 IP,SQLite 主从或共享存储;或引入单机崩溃后的简单冷备切换。

### 5.3 P2:增强可观测性与体验

| 改进 | 说明 |
|------|------|
| 端到端质量矩阵 | 每节点周期互 ping,输出 latency/loss 表到 admin console |
| relay 水位监控 | 端口池占用、队列丢弃、扇出次数打点,暴露 Prometheus |
| 在线告警 | NAS 离线/大丢包时 admin 收到通知(webhook/邮件) |
| 客户端路径标识 | UI 显示"当前路径:直连/中继 + 延迟",便于排查 |
| 后台保活优化 | 前台密集轮询,后台降频(5-15s)+ 系统省电白名单引导 |

### 5.4 P3:架构级替代方案评估

见下节。

---

## 六、更好的方案对比

### 方案 A:保持自研，参考 Swift/Tailscale 控制面模型

- **思想**:完全解耦"控制面(协调)"与"数据面(包转发)"。
- **改进**:
  - 控制面仍为你的 HTTPS server,但数据面协议扩展为 **STUN-like 端点发现 + 主动打洞 + relay 兜底**(DERP 模型)。
  - 客户端维护"好友映射",数据面流量优先直连,只有直连失败才经 relay。
  - 此模式正是 Tailscale/Headscale 的成熟做法,自研实现的复杂度主要在打洞成功率的调优。
- **结论**:**推荐演进方向**,与现有架构兼容度高(开箱即有的 whoami/group 已是雏形),改动集中于客户端打洞调度 + 服务端多候选下发。

### 方案 B:引入开源 Overlay(Headscale + Tailscale 客户端)

- **优点**:打洞、DERP、ACL、SSO、多平台客户端全成熟,省去大量自研。
- **缺点**:引入外部依赖,node 管理/审批流被 Tailscale 语义接管,与现有邀请码/审批/设备绑定流程整合成本高;控制权外移。
- **结论**:可行但会重构现有产品范式,适合"重平台"演进而非当前阶段。

### 方案 C:混合 SD-WAN(WireGuard + 自定义)**(推荐)

**阶段 1(短期,1-2 周)**:完成 5.1 + 5.2.1(打洞主动化 + CGNAT 多出口收敛),三端延迟可望从 655ms 改善到直连 <100ms(同城)或 relay <120ms(靠 VPS)。
**阶段 2(中期,1 个月)**:多中继选优 + HA + 可观测性,数据面可靠性达"可用率 99.9%"。
**阶段 3(长期,选型)**:
- 若产品要面向多用户/多网段/多租户 → 演进到 A(Tailscale 模型)。
- 若保持轻量自部署 → 继续 C,补 ACL/IPv6/审计。

### 综合推荐

**继续自研(C 路径起步,向 A 演进)。** 理由:
1. 现有代码已验证单播路由、权限、审批、跨端互通,底层 WG 稳定;
2. 自研可完全掌控邀请/审批/绑定的产品范式;
3. 打洞 + 多中继是本项目当前最值得投入的两点,工作量可控;
4. 一旦需要多租户/大规模,再引入 Headscale 语义或直接换 Tailscale 也不晚。

---

## 七、总结

SNET 虚拟组网方案**架构可行、功能完备**，已实测打通三端互通，成员权限模型与 relay 单播路由设计成熟。主要短板集中在**单点中继依赖、打洞未常态化、CGNAT 多出口回程不稳、可观测性弱**。改进的第一优先级是"打洞主动化 + 多地址扇出 + 短 TTL 收敛"缓解 CGNAT 丢包，中期做多中继选优与 HA，长期按需向 Tailscale 控制面模型演进。

建议下一步:优先实现 5.1(P0 丢包缓解)与 5.2.1(打洞常态直连),随后验证延迟改善,再决定是否需要多中继。

---

## 八、C 路径实施进度(2026-09-24)

**决策**:选择 C 路径(自研演进)起步,优先 P0 丢包缓解,再向打洞常态直连演进。

### 8.1 P0 已落地并部署验证

**改动 1:relay 单播路由多地址扇出**(internal/server/store.go `RelayRouteNodePort`)
- 原逻辑:命中 sender 端口上**第一个**匹配 recipientHosts 的 flow 即 `return true` 单发。
- 问题:CGNAT 订阅者可能持有多个活跃公网映射,recipient 每用一个 egress 就对 sender 端口建一个 inbound flow;只发第一个映射,半数帧会落在对端不再读取的映射上(实测 ~50% 丢)。
- 修复:遍历 sender 端口全部 flows,凡 host ∈ recipientHosts 且尚未发送的全部 `relaySendFrom`,按 host 去重;发送失败即从候选剔除避免漏发。

**改动 2:反向索引 stale host 短 TTL 淘汰**(新增 `hostSeen` 字段 + `pruneStaleHostsLocked`)
- 原逻辑:`nodeByHost` 由 `learnNodeHostLocked` 只增不减,data-plane host 永不淘汰;运营商回收 CGNAT 池把地址分给新订阅者时,旧归属仍把帧导向旧 owner。
- 修复:新增 `hostSeen map[string]time.Time` 记录每个 host 最后活跃时刻;`OnRelayFlow` 每 ~8s 清扫时对闲置超 `hostStaleTTL`(5min)且无活跃 relay flow / 非当前 ctrlHost 的 host 从 `nodeByHost` + `hostSeen` 剔除。

**改动 3:打洞候选窗口排序修复**(internal/client/daemon.go `buildPeerCandidates`)
- 原实现把 observed 端点内联展开窗口,导致直接候选窗口排在 observed 窗口之后;单元测试断言(裸端点 → direct 窗口 → observed 窗口)失败(pre-existing)。
- 修复:先收集全部裸端点(v6 → direct → observed),再展开 direct 近端口窗口,最后展开 observed 窗口。**注意**:该修复属 P1 打洞范畴,随 P0 一并验证。

### 8.2 部署后实测结果(66.187.6.46 单点 relay)

| 方向 | 部署前 | 部署后(10 包×0.5s) | 提交版 sha |
|------|--------|---------------------|-----------|
| VPS docker → 手机 | **50% 丢** (2/4) | **0% 丢**, avg 338ms | server sha256 7fcfd16e |
| VPS docker → Mac | 曾 100% 丢 | **0% 丢**, avg 306ms | 同上 |
| Mac → 手机 | 0% 丢 | 0% 丢, avg 817ms | 同上 |
| Mac → VPS | 0% 丢 | 0% 丢, avg 322ms | 同上 |
| relay 15min 内到手机 UNICAST | — | **0 次失败** (全 ok=true) | 同上 |

relay 日志确认 `recipientHosts` 持续含手机双地址 `223.160.208.29 / 223.160.209.29`(控制面 HTTPS 走 209.29,数据面 UDP 走 208.29),扇出到当天活跃映射;旧映射(51659→41234→52030 轮换)由 TTL 平移淘汰。

### 8.3 直连(punch)现状与 P1 方向

- Mac daemon 日志确认**直连尝试在规律循环**:`peer 2DZ95HYB relay -> direct (||obs)` → 16s 内无握手 → `direct -> relay`。CGNAT 对称 NAT 下运营商每流独立分配端口,外部单向 punch 难以命中,属预期。
- 收益:即使打洞不中,**relay 扇出已保证 0% 丢**。下一阶段(阶段 2)再做多中继选优、打洞成功后切换 + 健康看门狗、HA,以及客户端路径 UI 标识。

### 8.4 代码提交

- 提交范围:internal/server/store.go + internal/server/relay_test.go + internal/client/daemon.go(含回归测试 TestMultiEgressFanOut、TestStaleDataPlaneHostPruned 与既有测试修复),随 commit 一并记录于 git log。

### 8.5 P1 多中继选优:协议 + 客户端选优(最小实现,已落地)

**形态确认**:第二个 relay 数据面如何提供?选**「协议+选优先行(最小实现)」**——第二个 relay 暂用同一 VPS 多地址/端口模拟,协议就绪后另行部署。中继间互不集群、仅共享同一协调协议,数据面在客户端切换。

**改动(服务端)**:
- `PeersResp` 新增 `RelayEndpoints []string`:全部候选 relay(primary 前,每候选 `host:relayPort`),single-relay 部署下该字段保持未设置(wire 格式向后兼容)。
- `-relay-alternates` 逗号分隔 flag + `Store.SetRelayAlternates()`,服务端注册额外 relay host 并随 `/peers` 下发;本进程只宣传不绑定备用 relay 套接字。
- 测试 `TestMultiRelayEndpointsAdvertised`:候选按 `[primary, altB, altC]` 下发(host 去重、同 relay port),single-relay 模式字段为空。

**改动(客户端)**:
- `selectRelay(nid, cands)`:throttle 到 `relaySelectSec=30s`,对每个候选执行真实 `whoami` RTT 测量;失败候选视为不可达跳过;全失败保留前选,后续 poll 重试。
- 选优策略 `pickRelayByRTT`:取最短 RTT 候选,但当前 relay 的 RTT 在 `1.5x+20ms` 容差内则保持不动(避免健康会话被噪声抖动);当前 relay 不可达/显著变慢时切到最优。
- 数据面整体迁移到所选 relay:`d.relayEP` 存选中端,`resolvePeerEndpoints` 用所选候选构造 per-peer relay 端点,WG `ApplyPeers` 拿到当前 relay `host:peerPort`,切换时清空 observed 列表强制重学习。
- 测试 `TestPickRelayByRTT`(纯策略判稳/切换)+ `TestSelectRelayProbesReachability`(真实 Relay 进程,达选不达者,弃死取活)。

**部署形态(另行)**:
```bash
# 主控制器(66.187.6.46)
server -relay-host 66.187.6.46 -relay-base 51820 -relay-count 256 -relay-alternates <relayB-ip>[,<relayC-ip>]
# 备用 relay(同一协调网络,独立实例)
server -relay-host <relayB-ip> -relay-base 51820 -relay-count 256 ...
```
备用 relay 需能访问同一 bbolt 拓扑(或后续做轻量拓扑推送);当前 P1 只交付协议与选优,数据面切到备用 relay 的端到端验证待备用实例部署后进行。

### 8.6 端到端验证:候选下发 + 选优 + 故障切换(66.187.6.46 单机,IPv6 作备 relay)

**部署**:主 server 加 `-relay-alternates 2606:65c0:20:493:8209:7d93:3956:e3dd`(VPS 自有公网 IPv6);客户端 snetd 换带 P1 选优逻辑的新构建。无新 server 代码——主 relay 端口本就双栈监听,IPv6 候选在同一进程同一 store 上应答 whoami,故可在不引入第二实例的情况下真实验证客户端选优与故障切换(数据面连续性部分属阶段 2,见下)。

**1. 协议(trace wire 确认)**:`GET /peers` 返回
`relayEndpoint: 66.187.6.46:51820`,`relayEndpoints: ['66.187.6.46:51820', '[2606:…]:51820']`。

**2. 实测故障切换序列(snetd 日志完整还原)**:

| 时刻 | 动作 | relay select 日志 |
|------|------|-------------------|
| 16:50:20 | 首次双候选探测 | `66.187.6.46:51820 (340ms) over `(选 v4 主) |
| 16:58:07 | `iptables DROP udp dport 51820`(模拟主 relay 死亡) | `[2606:…]:51820 (314ms) over `(切 v6 备) |
| 17:00:14 | 解封恢复 v4 | `66.187.6.46:51820 (293ms) over [2606:…]:51820`(回到主) |

切换后数据面在 v4 恢复 0% 丢(ping 手机 avg ~756ms);v6 leg 期间 peer endpoint 正确迁移到 `[2606…]:51820/51822`。

**3. 测试暴露并修复的关键 bug(relayRTT 陈旧缓存阻断切换)**:
- 现象:封掉 v4:51820 后 `relay group` 仍持续打 v4 且 4 分钟不切换。
- 根因:`selectRelay` 探测失败时仅 `continue`,不清理 `relayRTT`;`pickRelayByRTT` 拿到死 relay 的旧 RTT(如 340ms)与最优(314ms)比较,`340 < 314*1.5+20=491` 落入"容差内"而拒切。
- 修复:探测失败即 `delete(relayRTT, key)`;每次探测同时清理本网络不再广告的候选;`bestRTT` 初始化为 `math.MaxInt64` 替代 0 哨兵(避免 0ms 候选锁死最优)。
- 回归测试 `TestSelectRelayDiesAfterSelection`:先选中活 relay,再让该 relay 死亡,断言切到另一候选且死 relay 的缓存被清。

**4. 已知边界(documented,阶段 2)**:任一备 relay 的**数据面完整串通**需要它以相同的 bbolt 拓扑路由(relay 按 v4 host 归因 sender;IPv6 源帧 `senderID=""` 无法回程)。本验证证明的是**选优 + 故障切换决策**;跨 relay 数据连续属阶段 2 拓扑推送课题。