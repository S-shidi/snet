#!/bin/bash
# macOS snetd daemon migration: vnet -> snet
# Run with: sudo bash deploy/mac-daemon-migrate.sh

set -euo pipefail

SNET_BIN="/usr/local/snet/bin"
SNET_DIR="/usr/local/snet"
OLD_VNET_DIR="/usr/local/vnet"
OLD_LABEL="com.vnet.daemon"
NEW_LABEL="com.snet.daemon"

echo "=== macOS snetd 迁移 ==="

# 1. Stop old daemon
echo "[1/7] 停止旧 daemon ($OLD_LABEL)..."
launchctl bootout system/$OLD_LABEL 2>/dev/null || true
sleep 1

# 2. Create directories
echo "[2/7] 创建目录..."
mkdir -p "$SNET_BIN"
mkdir -p /Library/LaunchDaemons

# 3. Copy snetd binary (from build output)
BUILD_DIR="$(cd "$(dirname "$0")/.." && pwd)/target/release"
if [ -f "$BUILD_DIR/snetd" ]; then
    echo "[3/7] 复制 snetd 二进制..."
    cp "$BUILD_DIR/snetd" "$SNET_BIN/snetd"
    chmod +x "$SNET_BIN/snetd"
elif [ -f "$OLD_VNET_DIR/bin/vnetd" ]; then
    echo "[3/7] 从旧位置复制 vnetd 并重命名为 snetd..."
    cp "$OLD_VNET_DIR/bin/vnetd" "$SNET_BIN/snetd"
    chmod +x "$SNET_BIN/snetd"
else
    echo "[3/7] ERROR: 找不到 snetd 二进制"
    exit 1
fi

# 4. Copy config (with snet server address)
echo "[4/7] 更新配置..."
if [ -f /tmp/snet_daemon.json ]; then
    cp /tmp/snet_daemon.json "$SNET_DIR/daemon.json"
else
    echo "  ERROR: /tmp/snet_daemon.json 不存在"
    exit 1
fi

# 5. Create LaunchDaemon plist
echo "[5/7] 创建 LaunchDaemon plist..."
cat > "/Library/LaunchDaemons/$NEW_LABEL.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.snet.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/snet/bin/snetd</string>
        <string>-config</string>
        <string>/usr/local/snet/daemon.json</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/snetd.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/snetd.log</string>
    <key>ProcessType</key>
    <string>Background</string>
</dict>
</plist>
PLIST

# 6. Set permissions
echo "[6/7] 设置权限..."
chown root:wheel "/Library/LaunchDaemons/$NEW_LABEL.plist"
chmod 644 "/Library/LaunchDaemons/$NEW_LABEL.plist"
chown root:wheel "$SNET_BIN/snetd"
chmod 755 "$SNET_BIN/snetd"
chown root:wheel "$SNET_DIR/daemon.json"
chmod 600 "$SNET_DIR/daemon.json"

# 7. Load new daemon
echo "[7/7] 启动新 daemon..."
launchctl bootstrap system "/Library/LaunchDaemons/$NEW_LABEL.plist"
sleep 2

# Verify
echo ""
echo "=== 验证 ==="
if launchctl print system/$NEW_LABEL > /dev/null 2>&1; then
    echo "✅ 新 daemon 已加载: $NEW_LABEL"
else
    echo "❌ 新 daemon 加载失败"
    exit 1
fi

PID=$(pgrep -f "$SNET_BIN/snetd" 2>/dev/null || echo "")
if [ -n "$PID" ]; then
    echo "✅ snetd 正在运行 (PID: $PID)"
else
    echo "⚠️  snetd 进程未找到，可能需要手动启动"
fi

# Health check
HEALTH=$(curl -s -o /dev/null -w "%{http_code}" https://snet.uizhi.eu.org:8090/healthz --connect-timeout 5 2>/dev/null || echo "000")
if [ "$HEALTH" = "200" ]; then
    echo "✅ 新服务器连通性检查: OK"
else
    echo "⚠️  服务器连通性检查: HTTP $HEALTH"
fi

echo ""
echo "=== 完成 ==="
echo "二进制: $SNET_BIN/snetd"
echo "配置:   $SNET_DIR/daemon.json"
echo "日志:   /var/log/snetd.log"
echo "服务:   launchctl print system/$NEW_LABEL"
