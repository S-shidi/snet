#!/bin/bash
# SNET 客户端版本验证脚本
# 从 Mac 本地检查群晖 NAS 客户端版本

set -e

CTL_TOKEN_FILE="/usr/local/snet/ctl-token"
NETWORK_ID="${1:-W4NSYT6E}"
DEVICE_NAME="${2:-SDNAS}"

echo "=== SNET 客户端版本验证 ==="
echo "网络 ID: $NETWORK_ID"
echo "设备名称: $DEVICE_NAME"
echo

# 检查 ctl-token
if [ ! -f "$CTL_TOKEN_FILE" ]; then
    echo "❌ 错误: 未找到 ctl-token 文件"
    echo "请确认 Mac 客户端正在运行"
    exit 1
fi

CTL_TOKEN=$(cat "$CTL_TOKEN_FILE")

# 获取服务端版本
echo "📊 服务端版本..."
SERVER_VERSION=$(curl -s https://snet.uizhi.eu.org:8090/healthz 2>/dev/null | grep -o '"serverVersion":"[^"]*"' | grep -o '"[^"]*"' | tr -d '"')
if [ -n "$SERVER_VERSION" ]; then
    echo "✓ 服务端版本: $SERVER_VERSION"
else
    echo "⚠️  无法获取服务端版本"
fi
echo

# 获取客户端列表
echo "📋 检查客户端状态..."
PEERS=$(curl -s -H "X-Ctl-Token: $CTL_TOKEN" \
    "http://127.0.0.1:19432/ctl/peers?nid=$NETWORK_ID" 2>/dev/null)

if [ -z "$PEERS" ]; then
    echo "❌ 错误: 无法获取客户端列表"
    exit 1
fi

# 解析目标设备信息
DEVICE_INFO=$(echo "$PEERS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for peer in data.get('peers', []):
    if peer.get('deviceName') == '$DEVICE_NAME':
        print(json.dumps(peer, indent=2))
        break
" 2>/dev/null)

if [ -z "$DEVICE_INFO" ]; then
    echo "❌ 错误: 未找到设备 '$DEVICE_NAME'"
    echo "可用设备:"
    echo "$PEERS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for peer in data.get('peers', []):
    print(f\"  - {peer.get('deviceName', 'Unknown')}\")
"
    exit 1
fi

echo "✓ 找到设备: $DEVICE_NAME"
echo

# v0.11.0+ 特性检测
echo "🔍 版本特性检测..."
echo

echo "--- v0.11.0 特性 ---"
RELAY_PORT=$(echo "$DEVICE_INFO" | grep -o '"relayPort": [0-9]*' | grep -o '[0-9]*')
RELAY_FLOW=$(echo "$DEVICE_INFO" | grep -o '"relayFlow": "[^"]*"' | grep -o '"[^"]*"' | tr -d '"')

if [ -n "$RELAY_PORT" ] && [ "$RELAY_PORT" != "null" ]; then
    echo "✓ relayPort: $RELAY_PORT"
else
    echo "✗ relayPort: 缺失"
fi

if [ -n "$RELAY_FLOW" ] && [ "$RELAY_FLOW" != "null" ]; then
    echo "✓ relayFlow: $RELAY_FLOW"
else
    echo "✗ relayFlow: 缺失"
fi
echo

echo "--- v0.10.0 特性 ---"
ENDPOINT=$(echo "$DEVICE_INFO" | grep -o '"endpoint": "[^"]*"' | grep -o '"[^"]*"' | tr -d '"')
LOCAL_EP=$(echo "$DEVICE_INFO" | grep -o '"localEndpoint": "[^"]*"' | grep -o '"[^"]*"' | tr -d '"')

if [ -n "$ENDPOINT" ] && [ "$ENDPOINT" != "null" ]; then
    echo "✓ endpoint: $ENDPOINT"
else
    echo "✗ endpoint: 缺失"
fi

if [ -n "$LOCAL_EP" ] && [ "$LOCAL_EP" != "null" ]; then
    echo "✓ localEndpoint: $LOCAL_EP"
else
    echo "✗ localEndpoint: 缺失"
fi
echo

# 版本判定
echo "📊 版本判定..."
if [ -n "$RELAY_PORT" ] && [ "$RELAY_PORT" != "null" ]; then
    VERSION="v0.11.0+"
    STATUS="✓ 最新"
elif [ -n "$ENDPOINT" ] && [ "$ENDPOINT" != "null" ]; then
    VERSION="v0.10.x"
    STATUS="⚠️  需要更新"
else
    VERSION="v0.9.x 或更早"
    STATUS="❌ 需要更新"
fi

echo "设备: $DEVICE_NAME"
echo "版本: $VERSION"
echo "状态: $STATUS"
echo

# 连接质量检测
echo "📈 连接质量..."
CTL_TOKEN=$(cat "$CTL_TOKEN_FILE" 2>/dev/null)
STATUS=$(curl -s -H "X-Ctl-Token: $CTL_TOKEN" \
    "http://127.0.0.1:19432/ctl/status" 2>/dev/null)

DEVICE_ID=$(echo "$PEERS" | grep -o '"id": "[^"]*"' | grep -B1 "$DEVICE_NAME" | head -1 | grep -o '"[^"]*"' | tr -d '"')

if [ -n "$DEVICE_ID" ]; then
    PEER_PATH=$(echo "$STATUS" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for net in data.get('networks', []):
    path = net.get('peerPaths', {}).get('$DEVICE_ID', {})
    if path:
        print(json.dumps(path, indent=2))
        break
" 2>/dev/null)
    
    if [ -n "$PEER_PATH" ]; then
        MODE=$(echo "$PEER_PATH" | grep -o '"mode": "[^"]*"' | grep -o '"[^"]*"' | tr -d '"')
        LOSS_RATE=$(echo "$PEER_PATH" | grep -o '"lossRate": [0-9.]*' | grep -o '[0-9.]*')
        BATCH=$(echo "$PEER_PATH" | grep -o '"batch": [0-9]*' | grep -o '[0-9]*')
        
        echo "连接模式: $MODE"
        echo "丢包率: ${LOSS_RATE:-0}%"
        
        if [ -n "$BATCH" ]; then
            echo "探测批次: $BATCH"
            echo "✓ 支持并发探测 (v0.11.0+)"
        fi
    fi
fi
echo

# 输出建议
echo "=== 建议 ==="
if [ "$STATUS" != "✓ 最新" ]; then
    echo "⚠️  设备版本过旧，建议更新："
    echo
    echo "1. 在项目根目录执行更新脚本："
    echo "   chmod +x scripts/update-nas.sh"
    echo "   ./scripts/update-nas.sh <群晖IP> admin"
    echo
    echo "2. 或者使用 Docker 更新："
    echo "   ssh admin@<群晖IP>"
    echo "   cd /volume1/docker/snetd"
    echo "   docker-compose down"
    echo "   docker-compose build --no-cache"
    echo "   docker-compose up -d"
    echo
else
    echo "✓ 设备版本最新，无需更新"
fi