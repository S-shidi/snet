#!/bin/bash
# Update the local (Mac) vnet daemon, migrate v1 config, restart launchd
# daemon. Also unloads the retired local coordination server (com.vnet.server)
# if a previous build had installed it. Requires sudo.
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN=/tmp/vnet-build

cd "$ROOT" || exit 1

echo "== build =="
go build -o "$BIN/vnetd" "$ROOT/cmd/client/vnetd"
go build -o "$BIN/vnetctl" "$ROOT/cmd/client/vnetctl"

echo "== install to /usr/local/vnet/bin =="
sudo install -m 755 "$BIN/vnetd" "$BIN/vnetctl" /usr/local/vnet/bin/

echo "== remove retired local server (if present) =="
if [ -f /Library/LaunchDaemons/com.vnet.server.plist ]; then
  sudo launchctl bootout system/com.vnet.server 2>/dev/null || true
  sudo rm -f /Library/LaunchDaemons/com.vnet.server.plist
  sudo rm -f /usr/local/vnet/bin/server
  echo "local server unloaded"
fi

echo "== restart daemon (v1 config migrates to v2 on next start) =="
sudo launchctl bootout system/com.vnet.daemon 2>/dev/null || true
sudo launchctl bootstrap system /Library/LaunchDaemons/com.vnet.daemon.plist 2>/dev/null || true
sleep 2

echo "== status =="
/usr/local/vnet/bin/vnetctl status | head -20

echo "== device identity =="
cat /usr/local/vnet/device.id 2>/dev/null || echo "(none yet — will be created on first daemon start)"
echo "== done =="
