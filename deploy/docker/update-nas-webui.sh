#!/bin/bash
# SNET 客户端更新脚本 - 群晖 NAS Docker
# 包含 Web UI 支持

set -e

echo "==================================="
echo "SNET 客户端更新 - 群晖 NAS"
echo "==================================="
echo ""

# 配置
CONTAINER_NAME="snet-client"
IMAGE_NAME="snet-client"
WORK_DIR="/volume1/docker/snet"
DATA_DIR="$WORK_DIR/data"

# 检查是否在群晖 NAS 上运行
if [ ! -d "/volume1" ]; then
    echo "错误: 此脚本必须在群晖 NAS 上运行"
    exit 1
fi

echo "步骤 1: 创建工作目录"
mkdir -p "$WORK_DIR"
mkdir -p "$DATA_DIR"

echo "步骤 2: 停止现有容器"
if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    echo "停止容器 $CONTAINER_NAME..."
    docker stop "$CONTAINER_NAME" || true
    docker rm "$CONTAINER_NAME" || true
    echo "✓ 容器已停止并删除"
else
    echo "容器不存在，跳过"
fi

echo ""
echo "步骤 3: 拉取最新镜像"
echo "注意: 如果没有预构建镜像，将使用本地构建"

# 检查是否有预构建镜像
if docker images --format '{{.Repository}}:{{.Tag}}' | grep -q "${IMAGE_NAME}:latest"; then
    echo "使用现有镜像: ${IMAGE_NAME}:latest"
else
    echo "警告: 没有找到预构建镜像"
    echo "请确保已经构建了包含 Web UI 的镜像"
    echo ""
    echo "构建方法:"
    echo "  cd $WORK_DIR"
    echo "  git clone https://github.com/S-shidi/snet.git"
    echo "  cd snet"
    echo "  docker build -f deploy/docker/Dockerfile -t snet-client:latest ."
    exit 1
fi

echo ""
echo "步骤 4: 创建 docker-compose.yml"
cat > "$WORK_DIR/docker-compose.yml" << 'EOF'
version: '3.8'

services:
  snet-client:
    image: snet-client:latest
    container_name: snet-client
    restart: unless-stopped
    
    # 权限配置
    privileged: true
    
    # 网络配置
    # network_mode: host  # 如果使用 host 模式，注释掉 ports 部分
    
    # 端口映射
    ports:
      # Web UI (HTTP)
      - "8080:8080"
      # Web UI (HTTPS) - 可选
      # - "8443:8443"
      # WireGuard UDP: 每个网络占用一个端口
      - "52100-52163:52100-52163/udp"
    
    # 数据持久化
    volumes:
      - ./data:/data
    
    # 环境变量
    environment:
      # 协调服务器地址（首次运行自动绑定）
      - SNET_SERVER=${SNET_SERVER:-https://snet.uizhi.eu.org:8090}
      
      # 设备授权码（首次运行自动绑定）
      # - SNET_BIND_CODE=your-code-here
      
      # 数据目录
      - SNET_DATA_DIR=/data
    
    # 健康检查
    healthcheck:
      test: ["CMD", "curl", "-f", "http://127.0.0.1:8080"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s

# 可选：使用 Docker 网络
# networks:
#   snet-net:
#     driver: bridge

# volumes:
#   snet-data:
EOF

echo "✓ docker-compose.yml 已创建"

echo ""
echo "步骤 5: 启动新容器"
cd "$WORK_DIR"

# 使用 docker-compose 启动
if command -v docker-compose >/dev/null 2>&1; then
    docker-compose up -d
elif docker compose version >/dev/null 2>&1; then
    docker compose up -d
else
    echo "错误: docker-compose 未安装"
    exit 1
fi

echo ""
echo "步骤 6: 等待容器启动"
sleep 10

echo "步骤 7: 验证容器状态"
docker ps --filter "name=$CONTAINER_NAME" --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

echo ""
echo "步骤 8: 测试 Web UI"
echo "尝试访问 http://127.0.0.1:8080..."
if curl -s -m 5 http://127.0.0.1:8080 >/dev/null 2>&1; then
    echo "✓ Web UI 可以访问"
else
    echo "⚠ Web UI 暂时无法访问，可能需要等待启动"
fi

echo ""
echo "==================================="
echo "更新完成！"
echo "==================================="
echo ""
echo "访问地址:"
echo "  物理网络: http://10.7.86.111:8080"
echo "  虚拟网络: http://10.88.1.4:8080"
echo ""
echo "查看日志:"
echo "  docker logs -f snet-client"
echo ""
echo "管理容器:"
echo "  docker start snet-client"
echo "  docker stop snet-client"
echo "  docker restart snet-client"