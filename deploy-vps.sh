#!/bin/bash
# Deploy script for VPS update
# Run this script to update the server

set -e

SERVER="66.187.6.46"
USER="root"
SERVER_BIN="/usr/local/snet/bin/server"
LOCAL_BIN="server_linux"

echo "=== 部署 SNET 服务器更新 ==="
echo ""

# Check if server binary exists
if [ ! -f "$LOCAL_BIN" ]; then
    echo "错误: 找不到本地二进制文件 $LOCAL_BIN"
    echo "请先编译: GOOS=linux GOARCH=amd64 go build -o server_linux ./cmd/server"
    exit 1
fi

echo "1. 上传二进制文件..."
scp "$LOCAL_BIN" "$USER@$SERVER:/tmp/server_linux"

echo "2. 停止服务..."
ssh "$USER@$SERVER" 'systemctl stop snet-server'

echo "3. 替换二进制..."
ssh "$USER@$SERVER" "cp /tmp/server_linux $SERVER_BIN && chmod +x $SERVER_BIN"

echo "4. 启动服务..."
ssh "$USER@$SERVER" 'systemctl start snet-server'

echo "5. 等待服务启动..."
sleep 3

echo "6. 检查服务状态..."
ssh "$USER@$SERVER" 'systemctl status snet-server --no-pager -l'

echo ""
echo "=== 部署完成 ==="
echo "服务器: $SERVER"
echo "服务状态: $(ssh "$USER@$SERVER" 'systemctl is-active snet-server')"