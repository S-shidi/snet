#!/bin/bash
# install-mac.sh — Install CLI daemon + GUI app after build
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/desktop/src-tauri/target/release/bundle/macos/Snet.app"

echo "=== install CLI daemon ==="
sudo install -m 755 /tmp/snet-build/snetd /tmp/snet-build/snetctl /usr/local/snet/bin/

echo "=== restart launchd daemon ==="
sudo launchctl bootout system/com.snet.daemon 2>/dev/null || true
sudo launchctl bootstrap system /Library/LaunchDaemons/com.snet.daemon.plist
sleep 2
/usr/local/snet/bin/snetctl status | head -10

echo "=== install GUI app ==="
osascript -e 'quit app "Snet"' 2>/dev/null || true
sudo cp -R "$APP" /Applications/Snet.app
open /Applications/Snet.app

echo "=== done ==="
