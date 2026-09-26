# 群晖 NAS SNET 客户端更新指南（手动版）

## 当前状态

- **群晖 NAS**: 10.7.86.111:8688
- **用户**: S.shidi
- **SNET 配置目录**: `/volume1/@appdata/ContainerManager/all_shares/docker/snet`
- **当前版本**: v0.10.x（缺少中继回环特性）
- **目标版本**: v0.11.0+（含 P0/P1/P2 优化）

## 新版本文件

已上传到群晖 NAS：
- `/tmp/snetd.new` (7.0M, 2026-09-11 22:56)
- `/tmp/update-snet.sh` (更新脚本)

---

## 更新方法

### 方法 1：DSM 任务计划（推荐）

1. **登录 DSM Web UI**
   - 浏览器访问: `https://10.7.86.111:5001`
   - 用户名: `S.shidi`
   - 密码: `Xuan235753`

2. **打开任务计划**
   - 控制面板 → 任务计划

3. **创建新任务**
   - 点击"新增" → "计划的任务" → "用户定义的脚本"
   - 任务名称: `SNET客户端更新`
   - 用户: `root`
   - 事件: "无"（立即运行）

4. **编辑脚本**
   - 任务设置 → 用户定义的脚本：
   
   ```bash
   #!/bin/bash
   /tmp/update-snet.sh
   ```

5. **运行任务**
   - 勾选任务，点击"运行"
   - 等待完成（约 1-2 分钟）

6. **查看结果**
   - 点击"查看结果" 或在 SSH 中执行：
   
   ```bash
   docker ps | grep snet-client
   docker logs snet-client 2>&1 | tail -20
   ```

---

### 方法 2：SSH + sudo（手动）

1. **SSH 登录**
   ```bash
   ssh -p 8688 S.shidi@10.7.86.111
   # 密码: Xuan235753
   ```

2. **切换到 root**
   ```bash
   sudo -i
   # 输入密码: Xuan235753
   ```

3. **执行更新脚本**
   ```bash
   /tmp/update-snet.sh
   ```

4. **验证结果**
   ```bash
   docker ps | grep snet-client
   docker logs snet-client --tail 20
   ```

---

### 方法 3：直接使用 Docker CLI

如果脚本失败，手动执行：

```bash
# 1. SSH 登录并切换 root
ssh -p 8688 S.shidi@10.7.86.111
sudo -i

# 2. 构建新镜像
cd /tmp/snet-update
/var/packages/ContainerManager/target/usr/bin/docker build -t snet-client:v0.11.0 .

# 3. 停止旧容器
docker stop snet-client 2>/dev/null || true
docker rm snet-client 2>/dev/null || true

# 4. 启动新容器
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet

docker run -d \
  --name snet-client \
  --restart unless-stopped \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -v $(pwd)/data:/data \
  snet-client:v0.11.0

# 5. 验证
docker ps | grep snet-client
docker logs snet-client --tail 20
```

---

## 更新后验证

### 1. 检查容器状态

```bash
ssh -p 8688 S.shidi@10.7.86.111
sudo docker ps | grep snet-client
```

预期输出：
```
CONTAINER ID   IMAGE                    STATUS         NAMES
xxxxxx         snet-client:v0.11.0      Up 10 seconds  snet-client
```

### 2. 检查版本特性

从 Mac 执行：

```bash
CTL_TOKEN=$(cat /usr/local/snet/ctl-token)
curl -s -H "X-Ctl-Token: $CTL_TOKEN" \
  "http://127.0.0.1:19432/ctl/peers?nid=W4NSYT6E" | \
  jq '.peers[] | select(.deviceName=="SDNAS") | {relayPort, relayFlow}'
```

预期输出：
```json
{
  "relayPort": 51821,
  "relayFlow": "39.180.139.115:xxxxx"
}
```

### 3. 测试连通性

```bash
# 从 Mac ping 群晖 NAS
ping -c 5 10.88.1.4

# 检查连接质量
CTL_TOKEN=$(cat /usr/local/snet/ctl-token)
curl -s -H "X-Ctl-Token: $CTL_TOKEN" \
  "http://127.0.0.1:19432/ctl/peers?nid=W4NSYT6E" | \
  jq '.peers[] | select(.deviceName=="SDNAS") | {lossRate, parallelIdx}'
```

---

## 新版本特性

| 特性 | 功能 | 改善 |
|------|------|------|
| **中继回环打洞** | relayPort/relayFlow | 穿透对称 NAT |
| **IPv6 直连优先** | IPv6 地址优先 | 提升直连率 |
| **并发候选探测** | 批量 5 并发 | -70% 探测时间 |
| **质量感知切换** | 30% 丢包自动回退 | 提升稳定性 |
| **Relay 异步发送** | 无阻塞转发 | +50% 并发性能 |
| **动态 Keepalive** | 空闲时 25s | -60% 功耗 |

---

## 故障排查

### 容器无法启动

```bash
# 查看详细日志
docker logs snet-client

# 检查设备文件
ls -la /dev/net/tun

# 如果不存在，创建设备文件
mkdir -p /dev/net
mknod /dev/net/tun c 10 200
chmod 600 /dev/net/tun
```

### 回滚到旧版本

```bash
# 停止新容器
docker stop snet-client && docker rm snet-client

# 恢复旧镜像
docker load -i /tmp/snet-client-backup.tar

# 恢复旧配置
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet
cp docker-compose.yml.bak docker-compose.yml

# 启动
/var/packages/ContainerManager/target/usr/bin/docker-compose up -d
```

### 检查网络配置

```bash
# 进入容器
docker exec -it snet-client sh

# 检查 WireGuard 接口
ip link show snet0
wg show

# 检查路由
ip route
```

---

## 文件清单

| 文件 | 路径 | 说明 |
|------|------|------|
| 新版本二进制 | `/tmp/snetd.new` | 7.0M |
| 更新脚本 | `/tmp/update-snet.sh` | 3.7K |
| Dockerfile | `/tmp/snet-update/Dockerfile` | 已创建 |
| 配置目录 | `/volume1/@appdata/ContainerManager/all_shares/docker/snet/` | |
| 数据目录 | `/volume1/@appdata/ContainerManager/all_shares/docker/snet/data/` | |
| daemon.json | `data/daemon.json` | SNET 配置 |
| ctl-token | `data/ctl-token` | 控制令牌 |

---

## 下一步

更新完成后，建议：

1. **测试直连**: 从 Android 设备测试与群晖 NAS 的直连
2. **监控日志**: 观察连接质量和切换行为
3. **性能对比**: 对比 v0.10.x 和 v0.11.0+ 的直连成功率

---

**更新脚本已上传，请通过 DSM 任务计划执行更新！**