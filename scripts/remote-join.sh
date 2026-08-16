#!/bin/bash
# Remote join helper for the SECOND Mac.
# Usage (on the second Mac, as admin):
#   sudo ./remote-join.sh SERVER_IP "vnet://join?nid=...&code=..." [PORT] [CA_PATH]
#   e.g. sudo ./remote-join.sh 66.187.6.46 "vnet://join?nid=AEVKEXVS&code=..." 51821 /usr/local/vnet/certs/server.pem
# CA_PATH pins the coordination server's TLS certificate. When omitted the
# join falls back to plain HTTP (or the server's certificate is trusted via
# the default store if the scheme is https).
set -e

SERVER_IP="$1"
LINK="$2"
PORT="${3:-51821}"
CA_PATH="$4"

if [ -z "$SERVER_IP" ] || [ -z "$LINK" ]; then
    echo "usage: $0 SERVER_IP LINK [PORT] [CA_PATH]" >&2
    exit 2
fi

SCHEME="http"
CA_FLAG=""
if [ -n "$CA_PATH" ]; then
    if [ ! -f "$CA_PATH" ]; then
        echo "error: CA certificate not found: $CA_PATH" >&2
        exit 2
    fi
    SCHEME="https"
    CA_FLAG="--ca-path $CA_PATH"
fi

INSTALL=/usr/local/vnet
mkdir -p "$INSTALL/bin"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cp "$SCRIPT_DIR/bin/server" "$SCRIPT_DIR/bin/vnetd" "$SCRIPT_DIR/bin/vnetctl" "$INSTALL/bin/"

# Authorize inbound connections (unsigned binary would otherwise be firewall-blocked)
/usr/libexec/ApplicationFirewall/socketfilterfw --add "$INSTALL/bin/vnetd" >/dev/null 2>&1 || true
/usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp "$INSTALL/bin/vnetd" >/dev/null 2>&1 || true

pkill -f "$INSTALL/bin/vnetd" 2>/dev/null || true
"$INSTALL/bin/vnetd" -ctl 127.0.0.1:19432 -config "$INSTALL/daemon.json" >"$INSTALL/daemon.log" 2>&1 &
sleep 1

# shellcheck disable=SC2086
"$INSTALL/bin/vnetctl" --ctl http://127.0.0.1:19432 join --server "$SCHEME://$SERVER_IP:8090" --port "$PORT" --link "$LINK" $CA_FLAG
echo ""
echo "== status =="
"$INSTALL/bin/vnetctl" --ctl http://127.0.0.1:19432 status
