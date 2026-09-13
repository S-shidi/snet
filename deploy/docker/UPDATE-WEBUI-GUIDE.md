# 群晖 NAS SNET 客户端 Web UI 更新指南

## 问题说明

当前容器运行的是旧版本，**没有包含 Web UI 和 Nginx**，因此端口 8080 无法访问。

---

## 更新方法

### 方法 1: 通过 DSM Web 界面（推荐）

#### 步骤 1: 登录 DSM

1. 打开浏览器
2. 访问: `http://10.7.86.111:5000`
3. 输入用户名和密码登录

#### 步骤 2: 打开 Container Manager

1. 点击主菜单（左上角 9 个点图标）
2. 找到并点击 **Container Manager**

#### 步骤 3: 停止并删除旧容器

1. 在容器列表中找到 `snet-client`
2. 点击容器名称进入详情
3. 点击 **停止** 按钮
4. 停止后，点击 **删除** 按钮
5. **注意**: 不要删除数据卷（`/volume1/docker/snet/data`）

#### 步骤 4: 下载最新镜像

**选项 A: 从项目构建**

如果项目已经在群晖 NAS 上:
```bash
# SSH 登录到群晖 NAS
ssh -p 8688 S.shidi@10.7.86.111

# 切换到 root
su -

# 进入项目目录
cd /volume1/docker/snet

# 构建新镜像（包含 Web UI）
docker build -f deploy/docker/Dockerfile -t snet-client:latest .
```

**选项 B: 从 Mac 上传镜像**

在 Mac 上构建并上传：
```bash
# 1. 构建镜像
cd /Users/shidi/OpenWork/Snet
docker build -f deploy/docker/Dockerfile -t snet-client:latest .

# 2. 保存镜像为 tar 文件
docker save snet-client:latest | gzip > snet-client.tar.gz

# 3. 上传到群晖 NAS
scp -P 8688 snet-client.tar.gz S.shidi@10.7.86.111:/volume1/docker/snet/

# 4. SSH 登录并加载镜像
ssh -p 8688 S.shidi@10.7.86.111
su -
docker load < /volume1/docker/snet/snet-client.tar.gz
```

#### 步骤 5: 创建新容器

在 DSM Container Manager 中:

1. 点击 **新增** → **创建容器**
2. 选择镜像: `snet-client:latest`
3. 配置容器:
   - **名称**: `snet-client`
   - **特权模式**: 启用
   - **自动重启**: 启用
4. 配置端口映射:
   - `8080:8080` (TCP)
   - `52100-52163:52100-52163` (UDP)
5. 配置卷映射:
   - 主机: `/volume1/docker/snet/data`
   - 容器: `/data`
6. 配置环境变量:
   - `SNET_SERVER`: `https://snet.uizhi.eu.org:8090`
7. 点击 **下一步** → **完成**

#### 步骤 6: 验证 Web UI

容器启动后:
1. 打开浏览器
2. 访问: `http://10.7.86.111:8080`
3. 应该能看到 SNET Web 控制界面

---

### 方法 2: 通过 SSH 命令行

如果可以通过 SSH 访问:

```bash
# 1. SSH 登录
ssh -p 8688 S.shidi@10.7.86.111

# 2. 切换到 root
su -

# 3. 停止并删除旧容器
docker stop snet-client
docker rm snet-client

# 4. 下载更新脚本
cd /volume1/docker/snet
curl -O https://raw.githubusercontent.com/S-shidi/snet/main/deploy/docker/update-nas-webui.sh
chmod +x update-nas-webui.sh

# 5. 运行更新脚本
./update-nas-webui.sh

# 6. 验证
docker ps
curl http://127.0.0.1:8080
```

---

### 方法 3: 使用 docker-compose

如果群晖 NAS 上有 docker-compose:

```bash
# SSH 登录
ssh -p 8688 S.shidi@10.7.86.111
su -

# 进入工作目录
cd /volume1/docker/snet

# 创建 docker-compose.yml
cat > docker-compose.yml << 'EOF'
version: '3.8'
services:
  snet-client:
    image: snet-client:latest
    container_name: snet-client
    privileged: true
    restart: unless-stopped
    ports:
      - "8080:8080"
      - "52100-52163:52100-52163/udp"
    volumes:
      - ./data:/data
    environment:
      - SNET_SERVER=https://snet.uizhi.eu.org:8090
EOF

# 停止旧容器
docker-compose down

# 启动新容器
docker-compose up -d

# 查看日志
docker-compose logs -f
```

---

## 验证步骤

### 1. 检查容器状态

```bash
docker ps | grep snet-client
```

应该显示:
```
CONTAINER ID   NAMES          STATUS        PORTS
...            snet-client    Up X minutes  0.0.0.0:8080->8080/tcp, ...
```

### 2. 检查端口监听

```bash
netstat -tunlp | grep 8080
```

应该显示:
```
tcp  0  0  0.0.0.0:8080  LISTEN  ...
```

### 3. 测试 Web UI

```bash
curl -I http://127.0.0.1:8080
```

应该返回 HTTP 200。

### 4. 从 Mac 访问

在 Mac 上打开浏览器:
```
http://10.7.86.111:8080
```

---

## 故障排查

### 问题 1: 端口仍无法访问

**检查**:
```bash
# 检查容器日志
docker logs snet-client

# 检查 Nginx 是否启动
docker exec snet-client ps aux | grep nginx

# 检查端口
docker exec snet-client netstat -tunlp | grep 8080
```

**解决**:
- 确保镜像包含 Web UI（检查镜像构建时间）
- 重启容器: `docker restart snet-client`

### 问题 2: 容器无法启动

**检查日志**:
```bash
docker logs snet-client
```

**常见原因**:
- 镜像不完整（重新构建）
- 数据卷权限问题（检查 `/data` 目录）
- 端口冲突（检查 8080 是否被占用）

### 问题 3: Web UI 显示错误

**检查**:
- 守护进程是否启动: `docker exec snet-client ps aux | grep snetd`
- API 是否响应: `curl http://127.0.0.1:8080/ctl/status`

---

## 访问地址

更新成功后:

| 访问方式 | 地址 |
|---------|------|
| **物理网络** | `http://10.7.86.111:8080` |
| **虚拟网络** | `http://10.88.1.4:8080` |
| **DSM 管理** | `http://10.7.86.111:5000` |

---

## 下一步

1. 更新容器后，Web UI 应该可以访问
2. 可以在 Web UI 中管理网络
3. 查看成员列表和连接状态
4. 配置子网路由等

---

## 需要帮助？

如果更新过程中遇到问题，请告诉我：
1. 具体的错误信息
2. 容器日志输出
3. 你使用的方法

我会帮你解决！