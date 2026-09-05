#!/bin/sh
set -e

DATA_DIR="${SNET_DATA_DIR:-/data}"
CONFIG_FILE="$DATA_DIR/daemon.json"
DEVICE_ID_FILE="$DATA_DIR/device.id"
CTL_ADDR="127.0.0.1:19432"

mkdir -p "$DATA_DIR"

# Ensure /dev/net/tun exists (wireguard-go needs it)
if [ ! -e /dev/net/tun ]; then
    mkdir -p /dev/net
    mknod /dev/net/tun c 10 200
    chmod 600 /dev/net/tun
    echo "[entrypoint] Created /dev/net/tun"
fi

# Prefer the binary from the data volume if present (allows hot-upgrade
# via bind mount without rebuilding the image).
if [ -x "$DATA_DIR/snetd" ]; then
    cp "$DATA_DIR/snetd" /usr/local/bin/snetd
    echo "[entrypoint] Using snetd binary from data volume"
fi
if [ -x "$DATA_DIR/snetctl" ]; then
    cp "$DATA_DIR/snetctl" /usr/local/bin/snetctl
fi

# snetctl finds the ctl-channel token next to the daemon config. The daemon
# runtime dir may not be in snetctl's built-in candidate list (e.g. the Docker
# image uses /data), so point it there explicitly.
if [ -f "$DATA_DIR/ctl-token" ]; then
    export SNET_CTL_TOKEN_FILE="$DATA_DIR/ctl-token"
fi

# ---- Helper: wait for snetd control API ----
wait_for_ctl() {
    for i in $(seq 1 30); do
        if snetctl -ctl "http://$CTL_ADDR" status >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    echo "[entrypoint] ERROR: snetd control API not ready after 30s" >&2
    return 1
}

# ---- Helper: check if device is already bound ----
is_bound() {
    STATUS=$(snetctl -ctl "http://$CTL_ADDR" status 2>/dev/null || echo "")
    echo "$STATUS" | grep -q '"bound":true'
}

# ---- Auto-bind if env vars are set ----
if [ -n "$SNET_SERVER" ] && [ -n "$SNET_BIND_CODE" ]; then
    echo "[entrypoint] Starting snetd for bind..."

    # Start snetd in background (control API on localhost only)
    snetd -ctl "$CTL_ADDR" -config "$CONFIG_FILE" -device-id-file "$DEVICE_ID_FILE" &
    SNETD_PID=$!

    # Wait for control API to be ready
    if ! wait_for_ctl; then
        kill $SNETD_PID 2>/dev/null
        exit 1
    fi

    # Check if already bound
    if is_bound; then
        echo "[entrypoint] Device already bound, skipping bind"
    else
        echo "[entrypoint] Binding to $SNET_SERVER ..."
        if snetctl -ctl "http://$CTL_ADDR" bind --server "$SNET_SERVER" --code "$SNET_BIND_CODE"; then
            echo "[entrypoint] Bind successful"
        else
            echo "[entrypoint] WARN: bind failed (device may already be bound or code invalid)" >&2
        fi
    fi

    # Stop snetd — it will be restarted below with nginx
    kill $SNETD_PID 2>/dev/null
    wait $SNETD_PID 2>/dev/null || true
fi

# ---- Start snetd + nginx ----
echo "[entrypoint] Starting snetd + nginx..."

# Start snetd in background
snetd -ctl "$CTL_ADDR" -config "$CONFIG_FILE" -device-id-file "$DEVICE_ID_FILE" &
SNETD_PID=$!

# Wait for snetd control API
if ! wait_for_ctl; then
    kill $SNETD_PID 2>/dev/null
    exit 1
fi

echo "[entrypoint] snetd ready on $CTL_ADDR"

# Copy TLS certs from host if available (for nginx HTTPS)
# Host certs may be symlinks to Let's Encrypt — use -L to test, -e to copy dereferenced
CERT_DIR="$DATA_DIR/certs"
mkdir -p "$CERT_DIR"
if [ ! -f "$CERT_DIR/server.pem" ] && [ -L /host-certs/server.pem -o -f /host-certs/server.pem ]; then
    cp -L /host-certs/server.pem "$CERT_DIR/server.pem"
    cp -L /host-certs/server-key.pem "$CERT_DIR/server-key.pem"
    chmod 600 "$CERT_DIR/server-key.pem"
    echo "[entrypoint] Copied TLS certs from host (dereferenced)"
elif [ -f "$CERT_DIR/server.pem" ]; then
    echo "[entrypoint] TLS certs already in data volume"
fi

# Start nginx in foreground (keeps container alive)
echo "[entrypoint] Starting nginx on port 8080 (HTTP) + 8443 (HTTPS)..."
nginx -g "daemon off;" &
NGINX_PID=$!

# Trap signals: stop both processes
cleanup() {
    echo "[entrypoint] Shutting down..."
    kill $NGINX_PID 2>/dev/null
    kill $SNETD_PID 2>/dev/null
    wait $NGINX_PID 2>/dev/null || true
    wait $SNETD_PID 2>/dev/null || true
}
trap cleanup TERM INT

# Wait for either process to exit
wait -n $SNETD_PID $NGINX_PID 2>/dev/null || true
cleanup
