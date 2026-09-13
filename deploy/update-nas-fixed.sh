#!/bin/bash
# SNET 群晖 NAS 更新脚本（修复 HTTP 超时问题）
# 必须以 root 身份运行

echo "=== 构建新镜像 ==="
cd /tmp/snet-fixed
/var/packages/ContainerManager/target/usr/bin/docker build -t snet-client:v0.11.0-fixed .

echo ""
echo "=== 停止旧容器 ==="
/var/packages/ContainerManager/target/usr/bin/docker stop snet-client
/var/packages/ContainerManager/target/usr/bin/docker rm snet-client

echo ""
echo "=== 更新 docker-compose.yml ==="
cd /volume1/@appdata/ContainerManager/all_shares/docker/snet
cat > docker-compose.yml << 'EOF'
services:
  snet-client:
    image: snet-client:v0.11.0-fixed
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

echo ""
echo "=== 启动新容器 ==="
/var/packages/ContainerManager/target/usr/bin/docker-compose up -d

echo ""
echo "=== 验证 ==="
sleep 3
/var/packages/ContainerManager/target/usr/bin/docker ps | grep snet
/var/packages/ContainerManager/target/usr/bin/docker logs snet-client --tail 10

echo ""
echo "=== 完成 ==="
echo "版本: v0.11.0-fixed (HTTP 超时 30s)"