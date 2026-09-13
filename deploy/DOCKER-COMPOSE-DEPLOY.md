# 群晖 NAS Docker Compose 部署 SNET 客户端

## 前提条件

- 已上传新版本文件: `/tmp/snetd.new`
- 已创建 Dockerfile: `/tmp/snet-update/Dockerfile`

---

## 步骤 1: 准备镜像

```bash
# SSH 登录群晖
ssh -p 8688 S.shidi@10.7.86.111
# 密码: Xuan235753

# 切换 root
su -
# 密码: Xuan235753

# 确认文件存在
ls -lh /tmp/snetd.new
ls -la /tmp/snet-update/

# 构建新镜像
cd /tmp/snet-update
/var/packages/ContainerManager/target/usr/bin/docker build -t snet-client:v0.11.0 .

# 验证镜像
/var/packages/ContainerManager/target/usr/bin/docker images | grep snet
```

---

## 步骤 2: 更新 docker-compose.yml

```bash
# 进入配置目录
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet

# 备份原配置
cp docker-compose.yml docker-compose.yml.bak

# 创建新配置
cat > docker-compose.yml << 'EOF'
services:
  snet-client:
    image: snet-client:v0.11.0
    container_name: snet-client
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - ./data:/data
    environment:
      - SNET_SERVER=
      - SNET_BIND_CODE=
EOF

# 确认配置
cat docker-compose.yml
```

---

## 步骤 3: 停止旧容器

```bash
# 查看运行中的容器
/var/packages/ContainerManager/target/usr/bin/docker ps | grep snet

# 停止并删除
/var/packages/ContainerManager/target/usr/bin/docker stop snet-client
/var/packages/ContainerManager/target/usr/bin/docker rm snet-client

# 或使用 docker-compose
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet
/var/packages/ContainerManager/target/usr/bin/docker-compose down
```

---

## 步骤 4: 启动新容器

### 方法 A: 使用 docker-compose

```bash
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet

# 启动
/var/packages/ContainerManager/target/usr/bin/docker-compose up -d

# 查看日志
/var/packages/ContainerManager/target/usr/bin/docker-compose logs -f
```

### 方法 B: 直接使用 docker run

```bash
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet

/var/packages/ContainerManager/target/usr/bin/docker run -d \
  --name snet-client \
  --restart unless-stopped \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/net/tun:/dev/net/tun \
  -v $(pwd)/data:/data \
  snet-client:v0.11.0
```

---

## 步骤 5: 验证运行

```bash
# 检查容器状态
/var/packages/ContainerManager/target/usr/bin/docker ps | grep snet

# 查看日志
/var/packages/ContainerManager/target/usr/bin/docker logs snet-client

# 进入容器检查
/var/packages/ContainerManager/target/usr/bin/docker exec -it snet-client sh
ls -la /usr/local/bin/snetd
```

---

## 常见问题

### 问题 1: /dev/net/tun 不存在

```bash
# 创建设备文件
mkdir -p /dev/net
mknod /dev/net/tun c 10 200
chmod 600 /dev/net/tun
```

### 问题 2: 权限被拒绝

确保以 **root** 用户执行所有命令：
```bash
su -
```

### 问题 3: 镜像构建失败

检查 Dockerfile 和 snetd 文件：
```bash
cat /tmp/snet-update/Dockerfile
ls -lh /tmp/snetd.new
```

### 问题 4: 容器无法启动

查看详细日志：
```bash
/var/packages/ContainerManager/target/usr/bin/docker logs snet-client 2>&1
```

---

## 配置文件说明

### docker-compose.yml 参数

| 参数 | 说明 |
|------|------|
| `image` | 镜像名称，使用本地构建的 v0.11.0 |
| `container_name` | 容器名称 |
| `restart` | 重启策略: unless-stopped (异常退出时自动重启) |
| `network_mode: host` | 使用主机网络，避免端口映射 |
| `cap_add: NET_ADMIN` | 网络管理权限，用于 WireGuard |
| `devices: /dev/net/tun` | TUN 设备，WireGuard 必需 |
| `volumes: ./data` | 数据目录，包含配置和令牌 |
| `environment` | 环境变量 (留空，使用 data 目录的配置) |

---

## 数据目录结构

```
/volume1/@appdata/ContainerManager/all_shares/docker/snet/data/
├── daemon.json    # 客户端配置
├── ctl-token      # 控制令牌
├── device.id      # 设备 ID
└── certs/         # 证书目录
```

---

## 环境变量覆盖

如果需要通过环境变量配置：

```yaml
environment:
  - SNET_SERVER=https://snet.uizhi.eu.org:8090
  - SNET_BIND_CODE=你的绑定码
```

但推荐使用 `data/daemon.json` 配置文件。

---

## 更新流程总结

```bash
# 1. 构建
cd /tmp/snet-update
/var/packages/ContainerManager/target/usr/bin/docker build -t snet-client:v0.11.0 .

# 2. 停止
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet
/var/packages/ContainerManager/target/usr/bin/docker-compose down

# 3. 启动
/var/packages/ContainerManager/target/usr/bin/docker-compose up -d

# 4. 验证
/var/packages/ContainerManager/target/usr/bin/docker-compose logs -f
```

---

## 回滚到旧版本

如果新版本有问题：

```bash
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet

# 停止新容器
/var/packages/ContainerManager/target/usr/bin/docker-compose down

# 恢复旧配置
cp docker-compose.yml.bak docker-compose.yml

# 启动旧版本
/var/packages/ContainerManager/target/usr/bin/docker-compose up -d
```