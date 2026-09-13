# SNET 网络连接问题诊断和解决方案

## 问题诊断

### 症状
- Mac SNET 客户端无法稳定连接到服务器 API
- 日志显示：`context deadline exceeded (Client.Timeout exceeded while awaiting headers)`
- 所有节点无法 Ping 通
- 100% 丢包

### 根本原因
1. **服务器 API 响应超时** - 客户端无法及时获取 peers 信息
2. **网络连接不稳定** - 可能是服务器负载或网络路由问题
3. **relayPort/relayFlow 无法获取** - 导致 v0.11.0 特性无法工作

---

## 解决方案

### 方案 1: 增加客户端超时时间（推荐）

修改 SNET 客户端配置，增加 HTTP 超时：

```json
{
  "serverAddr": "https://snet.uizhi.eu.org:8090",
  "wireguardPort": 51820,
  "httpClient": {
    "timeout": "30s",
    "retryCount": 3,
    "retryDelay": "5s"
  }
}
```

### 方案 2: 检查服务器状态

```bash
# 检查服务器健康状态
curl -v https://snet.uizhi.eu.org:8090/healthz

# 检查服务器响应时间
curl -w "Time: %{time_total}s\n" https://snet.uizhi.eu.org:8090/healthz
```

### 方案 3: 使用备用服务器

如果主服务器不稳定，可以：
1. 部署备用 SNET 服务器
2. 修改客户端配置指向备用服务器
3. 实现自动故障转移

### 方案 4: 优化网络路由

```bash
# 检查到服务器的路由
traceroute 66.187.6.46

# 检查 DNS 解析
nslookup snet.uizhi.eu.org

# 测试不同地区的连接质量
ping -c 10 66.187.6.46
```

### 方案 5: 重置 WireGuard 连接

```bash
# 在 Mac 上重置
sudo pkill snetd
sudo launchctl unload /Library/LaunchDaemons/com.snet.daemon.plist
sudo launchctl load -w /Library/LaunchDaemons/com.snet.daemon.plist

# 在群晖上重置
docker restart snet-client
```

---

## 临时解决方案

### 使用直接 IP 连接

如果 DNS 解析有问题，可以：

1. **修改 hosts 文件**:
   ```
   66.187.6.46 snet.uizhi.eu.org
   ```

2. **或直接使用 IP**:
   ```json
   {
     "serverAddr": "https://66.187.6.46:8090"
   }
   ```

---

## 验证步骤

### 1. 检查服务器连接

```bash
# 从 Mac
curl -w "响应时间: %{time_total}s\n" https://snet.uizhi.eu.org:8090/healthz

# 从群晖
docker exec snet-client wget -O- https://snet.uizhi.eu.org:8090/healthz
```

### 2. 检查客户端状态

```bash
CTL_TOKEN=$(cat /usr/local/snet/ctl-token)
curl -s -H "X-Ctl-Token: $CTL_TOKEN" http://127.0.0.1:19432/ctl/status | jq .
```

### 3. 测试连接

```bash
ping -c 5 10.88.1.4
```

---

## 长期优化建议

### 1. 服务器端优化

- 增加服务器带宽
- 优化 API 响应时间
- 实现负载均衡
- 添加健康检查和自动重启

### 2. 客户端优化

- 增加重试机制
- 实现连接池
- 添加断线重连
- 本地缓存 peers 信息

### 3. 网络优化

- 使用 CDN 加速
- 优化路由
- 实现多服务器冗余

---

## 监控建议

### 1. 添加监控脚本

```bash
#!/bin/bash
# /usr/local/bin/snet-monitor.sh

LOG_FILE="/var/log/snet-monitor.log"
ALERT_THRESHOLD=5

FAIL_COUNT=$(grep -c "context deadline exceeded" /var/log/snetd.log | tail -1)

if [ "$FAIL_COUNT" -gt "$ALERT_THRESHOLD" ]; then
    echo "$(date): SNET 连接失败次数: $FAIL_COUNT，重启客户端" >> $LOG_FILE
    sudo launchctl unload /Library/LaunchDaemons/com.snet.daemon.plist
    sleep 2
    sudo launchctl load -w /Library/LaunchDaemons/com.snet.daemon.plist
fi
```

### 2. 添加 cron 任务

```bash
*/5 * * * * /usr/local/bin/snet-monitor.sh
```

---

## 下一步行动

1. **立即**: 检查服务器状态和响应时间
2. **短期**: 增加客户端超时和重试
3. **中期**: 部署备用服务器
4. **长期**: 优化整体网络架构

---

**需要我帮你实施哪个解决方案？**