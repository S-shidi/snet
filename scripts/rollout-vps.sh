#!/bin/bash
# Upload the new server to the Hostodo VPS, set admin credentials, restart.
# Usage: SNET_ADMIN_USER=admin SNET_ADMIN_PASSWORD='...' [SNET_ADMIN_TOKEN='...'] [SNET_REQUIRE_DEVICE_AUTH=1] scripts/rollout-vps.sh [HOST]
# Requires SSH key access to root@66.187.6.46 (scp + ssh, no password prompt).
# SNET_ADMIN_TOKEN preserves the static bearer-token channel across the env
# rewrite (omit it and the line is dropped from /etc/snet-server.env).
# SNET_REQUIRE_DEVICE_AUTH=1 also enables the enrollment gate on the server
# (only devices that bound an admin-generated authorization code may join).
set -e
HOST="${1:-root@66.187.6.46}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/build/linux-amd64"

if [ ! -x "$BIN/server" ] || [ ! -x "$BIN/snetd" ] || [ ! -x "$BIN/snetctl" ]; then
    echo "missing linux binaries — run: GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $BIN/... ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl"
    exit 2
fi
if [ -z "$SNET_ADMIN_USER" ] || [ -z "$SNET_ADMIN_PASSWORD" ]; then
    echo "need SNET_ADMIN_USER and SNET_ADMIN_PASSWORD exported"
    exit 2
fi

echo "== scp binaries =="
# Upload to a temp name then rename: the running server binary cannot be
# overwritten in place, but mv (rename) atomically replaces it; the systemd
# unit execs the new inode on the next restart.
for bin in server snetd snetctl; do
    scp "$BIN/$bin" "$HOST:/usr/local/snet/bin/.upload-$bin"
    ssh "$HOST" "mv -f /usr/local/snet/bin/.upload-$bin /usr/local/snet/bin/$bin && chmod 755 /usr/local/snet/bin/$bin"
done

echo "== write /etc/snet-server.env =="
ENV_LINES="SNET_ADMIN_USER=%s\nSNET_ADMIN_PASSWORD=%s\n"
ENV_ARGS="'$SNET_ADMIN_USER' '$SNET_ADMIN_PASSWORD'"
if [ -n "$SNET_ADMIN_TOKEN" ]; then
    ENV_LINES="${ENV_LINES}SNET_ADMIN_TOKEN=%s\n"
    ENV_ARGS="${ENV_ARGS} '$SNET_ADMIN_TOKEN'"
fi
if [ -n "$SNET_REQUIRE_DEVICE_AUTH" ]; then
    ENV_LINES="${ENV_LINES}SNET_REQUIRE_DEVICE_AUTH=%s\n"
    ENV_ARGS="${ENV_ARGS} '$SNET_REQUIRE_DEVICE_AUTH'"
fi
ssh "$HOST" "umask 077; printf '$ENV_LINES' $ENV_ARGS > /etc/snet-server.env && cat /etc/snet-server.env"

echo "== install systemd unit + restart =="
scp "$ROOT/deploy/snet-server.service" "$HOST:/etc/systemd/system/snet-server.service"
ssh "$HOST" "systemctl daemon-reload && systemctl restart snet-server && sleep 1 && echo -n 'healthz: ' && curl -sk https://127.0.0.1:8090/healthz && echo && systemctl status snet-server --no-pager | head -6"

echo "== verify zombie TTL + relay =="
ssh "$HOST" "journalctl -u snet-server --since '-1min' --no-pager | grep -E 'zombie|relay|listen' | head -5"
echo "== done =="
