#!/bin/bash
# SNET 群晖 NAS 客户端更新脚本
# 用法: ./scripts/update-nas.sh <群晖IP> [用户名]

set -e

# 配置
NAS_HOST="${1}"
NAS_USER="${2:-admin}"
NAS_SCP_PATH="${3:-/volume1/docker/snetd}"
LOCAL_SNETD="snetd"

if [ -z "$NAS_HOST" ]; then
    echo "用法: $0 <群晖IP> [用户名] [远程路径]"
    echo "示例: $0 192.168.1.100 admin /volume1/docker/snetd"
    exit 1
fi

echo "=== SNET 群晖 NAS 客户端更新 ==="
echo "目标: $NAS_USER@$NAS_HOST:$NAS_SCP_PATH"
echo

# 检查本地是否为项目根目录
if [ ! -f "go.mod" ] || [ ! -d "cmd/client/snetd" ]; then
    echo "❌ 错误: 请在项目根目录执行此脚本"
    exit 1
fi

# 步骤 1: 构建最新版本
echo "📦 [1/5] 构建最新版本..."
go build -trimpath -ldflags="-s -w" -o "$LOCAL_SNETD" ./cmd/client/snetd

if [ ! -f "$LOCAL_SNETD" ]; then
    echo "❌ 构建失败: $LOCAL_SNETD 未生成"
    exit 1
fi

BINARY_SIZE=$(ls -lh "$LOCAL_SNETD" | awk '{print $5}')
BUILD_TIME=$(stat -f "%Sm" -t "%Y-%m-%d %H:%M:%S" "$LOCAL_SNETD")
echo "✓ 构建成功: $LOCAL_SNETD ($BINARY_SIZE, $BUILD_TIME)"
echo

# 步骤 2: 验证版本特性
echo "🔍 [2/5] 验证版本特性..."
if strings "$LOCAL_SNETD" | grep -q "0.11.0"; then
    echo "✓ 版本检查: 包含 v0.11.0 特性"
else
    echo "⚠️  版本检查: 未找到 v0.11.0 标记"
fi
echo

# 步骤 3: 上传到群晖 NAS
echo "📤 [3/5] 上传到群晖 NAS..."
echo "请输入 $NAS_USER@$NAS_HOST 的密码:"
scp "$LOCAL_SNETD" "$NAS_USER@$NAS_HOST:$NAS_SCP_PATH/snetd.new"

if [ $? -eq 0 ]; then
    echo "✓ 上传成功"
else
    echo "❌ 上传失败"
    exit 1
fi
echo

# 步骤 4: 远程替换并重启
echo "🔄 [4/5] 远程替换并重启容器..."
ssh "$NAS_USER@$NAS_HOST" << 'ENDSSH'
set -e

NAS_SCP_PATH="${NAS_SCP_PATH:-/volume1/docker/snetd}"

echo "停止容器..."
docker stop snetd 2>/dev/null || true

echo "备份旧版本..."
if [ -f "$NAS_SCP_PATH/snetd" ]; then
    cp "$NAS_SCP_PATH/snetd" "$NAS_SCP_PATH/snetd.backup.$(date +%Y%m%d_%H%M%S)"
fi

echo "替换二进制..."
mv "$NAS_SCP_PATH/snetd.new" "$NAS_SCP_PATH/snetd"
chmod +x "$NAS_SCP_PATH/snetd"

echo "复制到容器..."
docker cp "$NAS_SCP_PATH/snetd" snetd:/usr/local/bin/snetd

echo "启动容器..."
docker start snetd

echo "等待容器就绪..."
sleep 5

echo "检查容器状态..."
docker ps --filter name=snetd --format "table {{.Names}}\t{{.Status}}"
ENDSSH

if [ $? -eq 0 ]; then
    echo "✓ 远程更新成功"
else
    echo "❌ 远程更新失败"
    exit 1
fi
echo

# 步骤 5: 验证更新结果
echo "✅ [5/5] 验证更新结果..."
sleep 10

echo "检查群晖 NAS 连接状态..."
CTL_TOKEN=$(cat /usr/local/snet/ctl-token 2>/dev/null)
if [ -n "$CTL_TOKEN" ]; then
    STATUS=$(curl -s -H "X-Ctl-Token: $CTL_TOKEN" \
        "http://127.0.0.1:19432/ctl/peers?nid=W4NSYT6E" 2>/dev/null)
    
    RELAY_PORT=$(echo "$STATUS" | grep -o '"relayPort":[0-9]*' | head -1 | grep -o '[0-9]*')
    RELAY_FLOW=$(echo "$STATUS" | grep -o '"relayFlow":"[^"]*"' | head -1 | grep -o '"[^"]*"' | tr -d '"')
    
    if [ -n "$RELAY_PORT" ] || [ -n "$RELAY_FLOW" ]; then
        echo "✓ 群晖 NAS 已更新到 v0.11.0+"
        echo "  relayPort: $RELAY_PORT"
        echo "  relayFlow: $RELAY_FLOW"
    else
        echo "⚠️  未检测到 v0.11.0 特性，请手动验证"
    fi
else
    echo "⚠️  无法自动验证，请手动检查"
fi
echo

echo "=== 更新完成 ==="
echo "如需回滚，请在群晖 NAS 上执行："
echo "  docker stop snetd"
echo "  docker cp $NAS_SCP_PATH/snetd.backup.* snetd:/usr/local/bin/snetd"
echo "  docker start snetd"