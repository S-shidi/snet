# VPS 部署说明

## 问题诊断

SSH 密钥认证失败，可能的原因：
1. VPS 服务器上的 `authorized_keys` 未包含本机公钥
2. SSH 配置限制
3. 密钥权限问题

## 解决方案

### 方案 1: 添加公钥到 VPS（推荐）

**步骤：**

1. **获取本机公钥**
   ```bash
   cat ~/.ssh/id_ed25519_vnet.pub
   # 输出: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPeCRgRcWYR8Jv48lBi1WgRh+A4/tj7SBKesmxv7K0St vnet-deploy
   ```

2. **使用密码登录 VPS，添加公钥**
   ```bash
   # 使用密码登录
   ssh root@66.187.6.46
   
   # 在 VPS 上执行
   echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPeCRgRcWYR8Jv48lBi1WgRh+A4/tj7SBKesmxv7K0St vnet-deploy" >> ~/.ssh/authorized_keys
   
   # 修复权限
   chmod 600 ~/.ssh/authorized_keys
   
   # 退出
   exit
   ```

3. **测试密钥登录**
   ```bash
   ssh -i ~/.ssh/id_ed25519_vnet root@66.187.6.46 'echo "SSH OK"'
   ```

---

### 方案 2: 使用密码部署（临时）

**步骤：**

1. **使用 SCP 上传（会提示输入密码）**
   ```bash
   scp server_linux root@66.187.6.46:/tmp/server_linux
   # 输入密码
   ```

2. **使用 SSH 更新服务**
   ```bash
   ssh root@66.187.6.46
   # 输入密码
   
   # 在 VPS 上执行
   systemctl stop snet-server
   cp /tmp/server_linux /usr/local/snet/bin/server
   chmod +x /usr/local/snet/bin/server
   systemctl start snet-server
   systemctl status snet-server
   
   # 验证
   curl -sk https://localhost:8090/admin | grep -o "ipv6\|publicIPv4"
   ```

---

### 方案 3: 使用部署脚本（修复认证后）

**前提：** 先完成方案 1 添加公钥

**执行：**
```bash
./deploy-vps.sh
```

---

## 完整部署命令（手动）

```bash
# === 在本地机器执行 ===

# 1. 编译（已完成）
GOOS=linux GOARCH=amd64 go build -o server_linux ./cmd/server

# 2. 上传二进制
scp server_linux root@66.187.6.46:/tmp/server_linux
# 输入密码: [您的 VPS 密码]

# 3. SSH 登录
ssh root@66.187.6.46
# 输入密码: [您的 VPS 密码]

# === 在 VPS 服务器执行 ===

# 4. 停止服务
systemctl stop snet-server

# 5. 替换二进制
cp /tmp/server_linux /usr/local/snet/bin/server
chmod +x /usr/local/snet/bin/server

# 6. 启动服务
systemctl start snet-server

# 7. 检查状态
systemctl status snet-server --no-pager -l

# 8. 验证新功能
curl -sk https://localhost:8090/admin/networks/W4NSYT6E/nodes \
  -H "Authorization: Bearer $(cat /etc/snet-server.env | grep ADMIN_TOKEN | cut -d= -f2)" \
  | jq '.[0] | {ip, ipv6, publicIPv4, publicIPv6}'

# 9. 查看前端
curl -sk https://localhost:8090/admin | grep -o "虚拟 IP\|公网 IP"

# 10. 退出
exit
```

---

## 验证部署成功

**在浏览器访问：**
- URL: `https://66.187.6.46:8090/admin`
- 登录: `admin` / 密码（见 `/etc/snet-server.env`）

**检查点：**
- ✅ 点击网络列表中的"成员"按钮
- ✅ 成员列表显示"虚拟 IP"和"公网 IP"列
- ✅ 虚拟 IP 列同时显示 IPv4 和 IPv6
- ✅ 公网 IP 列显示设备的公网地址

**API 测试：**
```bash
# 登录获取 token
TOKEN=$(ssh root@66.187.6.46 'curl -sk -X POST https://localhost:8090/admin/login \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"admin\",\"password\":\"$(cat /etc/snet-server.env | grep ADMIN_PASSWORD | cut -d= -f2)\"}" \
  | jq -r .token')

# 获取节点列表
ssh root@66.187.6.46 "curl -sk https://localhost:8090/admin/networks/W4NSYT6E/nodes \
  -H 'Authorization: Bearer $TOKEN' \
  | jq '.[0] | {ip, ipv6, publicIPv4, publicIPv6}'"
```

---

## 功能说明

### 虚拟 IPv6 生成

**示例：**
- IPv4: `10.88.1.5`
- IPv6: `fd00:a:58:1::5`

**算法：**
- 取 IPv4 前 3 段转 16 进制作为前缀
- 最后一段作为主机部分

### 公网 IP 显示

**数据来源：**
- **IPv4**: 从 `RelayFlow` 或 `Endpoint` 提取
- **IPv6**: 从 `EndpointV6` 提取

**过滤规则：**
- 自动过滤私有/LAN 地址
- 仅显示公网可路由地址

---

## 故障排查

### SSH 连接失败

```bash
# 检查密钥权限
ls -la ~/.ssh/id_ed25519_vnet
# 应该是: -rw------- (600)

# 修复权限
chmod 600 ~/.ssh/id_ed25519_vnet

# 测试连接
ssh -vvv -i ~/.ssh/id_ed25519_vnet root@66.187.6.46 2>&1 | grep "Offering public key"
```

### 服务启动失败

```bash
# 检查日志
journalctl -u snet-server -n 50 --no-pager

# 检查二进制
file /usr/local/snet/bin/server
# 应该显示: ELF 64-bit LSB executable, x86-64

# 手动启动测试
/usr/local/snet/bin/server -h
```

### API 返回错误

```bash
# 检查服务状态
systemctl status snet-server

# 检查端口
ss -tlnp | grep 8090

# 检查数据库
ls -la /var/lib/snet/snet.db
```

---

## 本机公钥（用于添加到 VPS）

```
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPeCRgRcWYR8Jv48lBi1WgRh+A4/tj7SBKesmxv7K0St vnet-deploy
```

**添加到 VPS：**
```bash
ssh root@66.187.6.46
echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPeCRgRcWYR8Jv48lBi1WgRh+A4/tj7SBKesmxv7K0St vnet-deploy" >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
exit
```