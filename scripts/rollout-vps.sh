#!/bin/bash
# Upload the new server to the Hostodo VPS, set admin credentials, restart.
# Usage: VNET_ADMIN_USER=admin VNET_ADMIN_PASSWORD='...' [VNET_ADMIN_TOKEN='...'] [VNET_REQUIRE_DEVICE_AUTH=1] scripts/rollout-vps.sh [HOST]
# Requires SSH key access to root@66.187.6.46 (scp + ssh, no password prompt).
# VNET_ADMIN_TOKEN preserves the static bearer-token channel across the env
# rewrite (omit it and the line is dropped from /etc/vnet-server.env).
# VNET_REQUIRE_DEVICE_AUTH=1 also enables the enrollment gate on the server
# (only devices that bound an admin-generated authorization code may join).
set -e
HOST="${1:-root@66.187.6.46}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/build/linux-amd64"

if [ ! -x "$BIN/server" ] || [ ! -x "$BIN/vnetd" ] || [ ! -x "$BIN/vnetctl" ]; then
    echo "missing linux binaries — run: GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $BIN/... ./cmd/server ./cmd/client/vnetd ./cmd/client/vnetctl"
    exit 2
fi
if [ -z "$VNET_ADMIN_USER" ] || [ -z "$VNET_ADMIN_PASSWORD" ]; then
    echo "need VNET_ADMIN_USER and VNET_ADMIN_PASSWORD exported"
    exit 2
fi

echo "== scp binaries =="
# Upload to a temp name then rename: the running server binary cannot be
# overwritten in place, but mv (rename) atomically replaces it; the systemd
# unit execs the new inode on the next restart.
for bin in server vnetd vnetctl; do
    scp "$BIN/$bin" "$HOST:/usr/local/vnet/bin/.upload-$bin"
    ssh "$HOST" "mv -f /usr/local/vnet/bin/.upload-$bin /usr/local/vnet/bin/$bin && chmod 755 /usr/local/vnet/bin/$bin"
done

echo "== write /etc/vnet-server.env =="
ENV_LINES="VNET_ADMIN_USER=%s\nVNET_ADMIN_PASSWORD=%s\n"
ENV_ARGS="'$VNET_ADMIN_USER' '$VNET_ADMIN_PASSWORD'"
if [ -n "$VNET_ADMIN_TOKEN" ]; then
    ENV_LINES="${ENV_LINES}VNET_ADMIN_TOKEN=%s\n"
    ENV_ARGS="${ENV_ARGS} '$VNET_ADMIN_TOKEN'"
fi
if [ -n "$VNET_REQUIRE_DEVICE_AUTH" ]; then
    ENV_LINES="${ENV_LINES}VNET_REQUIRE_DEVICE_AUTH=%s\n"
    ENV_ARGS="${ENV_ARGS} '$VNET_REQUIRE_DEVICE_AUTH'"
fi
ssh "$HOST" "umask 077; printf '$ENV_LINES' $ENV_ARGS > /etc/vnet-server.env && cat /etc/vnet-server.env"

echo "== install systemd unit + restart =="
scp "$ROOT/deploy/vnet-server.service" "$HOST:/etc/systemd/system/vnet-server.service"
ssh "$HOST" "systemctl daemon-reload && systemctl restart vnet-server && sleep 1 && echo -n 'healthz: ' && curl -sk https://127.0.0.1:8090/healthz && echo && systemctl status vnet-server --no-pager | head -6"

echo "== verify zombie TTL + relay =="
ssh "$HOST" "journalctl -u vnet-server --since '-1min' --no-pager | grep -E 'zombie|relay|listen' | head -5"
echo "== done =="
