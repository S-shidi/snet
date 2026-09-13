# SNET 群晖 NAS 客户端更新指南

## 版本信息

| 组件 | 当前版本 | 最新版本 | 状态 |
|------|---------|---------|------|
| 服务端 | 0.11.0 | 0.11.0 | ✓ 最新 |
| 群晖 NAS | v0.10.x | 0.11.0+ | ⚠️ 需要更新 |
| Mac 客户端 | 0.11.0+ | 0.11.0+ | ✓ 最新 |

## 更新方法

### 方法 1：自动化脚本（推荐）

**适用场景**：有 SSH 访问权限的群晖 NAS

```bash
# 在项目根目录执行
chmod +x scripts/update-nas.sh
./scripts/update-nas.sh <群晖IP> admin

# 示例
./scripts/update-nas.sh 192.168.1.100 admin
```

**脚本功能**：
1. 自动构建最新版本（包含 P0/P1/P2 优化）
2. 上传到群晖 NAS
3. 停止容器、替换二进制、重启容器
4. 验证更新结果

---

### 方法 2：Docker Compose 更新

**适用场景**：使用 Docker Compose 管理的群晖 NAS

```bash
# SSH 登录群晖
ssh admin@<群晖IP>

# 进入 SNET 目录
cd /volume1/docker/snetd

# 停止容器
docker-compose down

# 方式 A：拉取最新代码重新构建
git pull
docker-compose build --no-cache

# 方式 B：从 Mac 上传新配置
# 在 Mac 上：
# scp deploy/docker/docker-compose.yml admin@<群晖IP>:/volume1/docker/snetd/

# 启动容器
docker-compose up -d

# 查看日志
docker-compose logs -f
```

---

### 方法 3：手动替换二进制

**适用场景**：需要精确控制更新过程

```bash
# 步骤 1: 在 Mac 上构建
cd /Users/shidi/OpenWork/Snet
go build -trimpath -ldflags="-s -w" -o snetd ./cmd/client/snetd

# 步骤 2: 上传到群晖
scp snetd admin@<群晖IP>:/volume1/docker/snetd/snetd.new

# 步骤 3: SSH 登录群晖
ssh admin@<群晖IP>

# 步骤 4: 替换二进制
docker stop snetd
docker cp /volume1/docker/snetd/snetd.new snetd:/usr/local/bin/snetd
docker start snetd

# 步骤 5: 清理
rm /volume1/docker/snetd/snetd.new
```

---

## 验证更新

### 从 Mac 验证（自动）

```bash
chmod +x scripts/check-nas-version.sh
./scripts/check-nas-version.sh W4NSYT6E SDNAS
```

**预期输出**：
```
✓ relayPort: 51821
✓ relayFlow: 39.180.139.115:xxxxx
✓ 支持并发探测 (v0.11.0+)
版本: v0.11.0+
状态: ✓ 最新
```

### 从 Mac 验证（手动）

```bash
CTL_TOKEN=$(cat /usr/local/snet/ctl-token)
curl -s -H "X-Ctl-Token: $CTL_TOKEN" \
  "http://127.0.0.1:19432/ctl/peers?nid=W4NSYT6E" | \
  jq '.peers[] | select(.deviceName=="SDNAS") | {relayPort, relayFlow}'
```

---

## 回滚方案

### Docker Compose 回滚

```bash
# SSH 登录群晖
ssh admin@<群晖IP>
cd /volume1/docker/snetd

# 查看备份镜像
docker images | grep snetd

# 回滚到备份版本
docker-compose down
docker tag snetd:backup snetd:latest
docker-compose up -d
```

### 手动回滚

```bash
# 停止容器
docker stop snetd

# 恢复备份二进制
docker cp /volume1/docker/snetd/snetd.backup.20260911 snetd:/usr/local/bin/snetd

# 启动容器
docker start snetd
```

---

## 新版本特性

### v0.11.0+ 新特性

| 特性 | 说明 | 收益 |
|------|------|------|
| **中继回环打洞** | relayPort/relayFlow 字段 | 穿透对称 NAT |
| **IPv6 直连优先** | 全局 IPv6 地址直连 | 无 NAT 直连 |
| **并发候选探测** | 5 候选/批次 | 握手时间 -70% |
| **质量感知切换** | 丢包率自动回退 | 避免劣质直连 |
| **Relay 异步发送** | 非阻塞发送 | 高并发性能 +50% |
| **Keepalive 动态** | 空闲 25s/活跃 10s | 移动端功耗 -60% |

### 版本对比

```
v0.10.x:
  ❌ 中继回环打洞缺失
  ❌ 并发探测缺失（顺序轮询 68s）
  ❌ 质量感知缺失（固定直连）
  
v0.11.0+:
  ✓ relayPort/relayFlow 自动打洞
  ✓ 批量并发探测（20s 最坏情况）
  ✓ 丢包率监控 + 自动回退
  ✓ Relay 异步 + 缓冲池
  ✓ Keepalive 动态调整
```

---

## 故障排查

### 容器无法启动

```bash
# 查看日志
docker logs snetd

# 常见问题：
# 1. 权限不足：添加 --privileged 或检查 cap_add
# 2. 设备不存在：检查 /dev/net/tun
# 3. 端口冲突：修改 docker-compose.yml 端口范围
```

### 连接不通

```bash
# 检查容器状态
docker ps -a

# 检查网络配置
docker exec snetd ip addr
docker exec snetd wg show

# 检查防火墙
# 群晖控制面板 → 终端机 → 防火墙
# 确保 UDP 52100-52163 已放行
```

### 版本不匹配

```bash
# 服务端版本检查
curl -s https://snet.uizhi.eu.org:8090/healthz | jq '.serverVersion'

# API 版本协商
# 客户端请求带 X-Snet-Api-Version: 1
# 服务端不匹配返回 426 (Upgrade Required)
```

---

## 更新日志

### v0.11.0 (2026-09-11)

**新功能**：
- 中继回环打洞（whoami/group）穿透对称 NAT
- IPv6 全局地址直连优先
- 并发候选探测（5 候选/批次）
- 质量感知路径切换（丢包 >30% 自动回退）

**优化**：
- Relay 异步发送 + 缓冲池
- Keepalive 动态调整（空闲 25s）

**修复**：
- 对称 NAT 候选盲投优化
- CGNAT 场景直连判定

---

## 联系支持

如遇问题，请提供以下信息：

```bash
# 收集诊断信息
echo "=== 服务端版本 ==="
curl -s https://snet.uizhi.eu.org:8090/healthz | jq '.'

echo "=== 客户端状态 ==="
CTL_TOKEN=$(cat /usr/local/snet/ctl-token)
curl -s -H "X-Ctl-Token: $CTL_TOKEN" http://127.0.0.1:19432/ctl/status | jq '.networks[0] | {name, peerPaths}'

echo "=== 容器日志 ==="
ssh admin@<群晖IP> "docker logs --tail 100 snetd"
```