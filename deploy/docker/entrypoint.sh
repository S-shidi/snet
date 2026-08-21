#!/bin/sh
set -e

DATA_DIR="${SNET_DATA_DIR:-/data}"
CONFIG_FILE="$DATA_DIR/daemon.json"
DEVICE_ID_FILE="$DATA_DIR/device.id"
CTL_ADDR="127.0.0.1:19432"

mkdir -p "$DATA_DIR"

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
    wait $SNETD_PID 2>/dev/null
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

# Start nginx in foreground (keeps container alive)
echo "[entrypoint] Starting nginx on port 8080..."
nginx -g "daemon off;" &
NGINX_PID=$!

# Trap signals: stop both processes
cleanup() {
    echo "[entrypoint] Shutting down..."
    kill $NGINX_PID 2>/dev/null
    kill $SNETD_PID 2>/dev/null
    wait $NGINX_PID 2>/dev/null
    wait $SNETD_PID 2>/dev/null
}
trap cleanup TERM INT

# Wait for either process to exit
wait -n $SNETD_PID $NGINX_PID 2>/dev/null
cleanup
