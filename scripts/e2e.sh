#!/bin/bash
# Single-host loopback e2e: server + two daemons + two utun + wireguard tunnel.
# Requires root (creates utun interfaces). Run: sudo scripts/e2e.sh
# Note: ctl ports are 29432+ and server/probe ports 8099+ to avoid colliding
# with a local production deployment (server :8090/:8091 probe, daemon ctl
# 19432, shared /usr/local/vnet/device.id) that may run on the same machine.
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD=/tmp/vnet-build
TEST=/tmp/vnet-e2e
CTLA=29432
CTLB=29433
CTLC=29434
SRV_PORT=8099
mkdir -p "$BUILD" "$TEST"

echo "== verifying prebuilt binaries =="
for bin in server vnetd vnetctl; do
    if [ ! -x "$BUILD/$bin" ]; then
        echo "missing $BUILD/$bin — run 'go build -o $BUILD/... ' first (as normal user)"
        exit 2
    fi
done

pkill -f "vnet-build/vnetd" 2>/dev/null || true
pkill -f "vnet-build/server" 2>/dev/null || true
sleep 0.5
# Clear stale daemon state from any previous run so enrollment/gate
# assertions start from a clean, unbound device.
rm -f "$TEST/a.json" "$TEST/b.json" "$TEST/c.json" "$TEST/c-gate.err" \
    "$TEST/device-a.id" "$TEST/device-b.id" "$TEST/device-c.id"

"$BUILD/server" -addr "127.0.0.1:$SRV_PORT" -probe-addr "127.0.0.1:8101" >"$TEST/server.log" 2>&1 &
SRV=$!
"$BUILD/vnetd" -ctl "127.0.0.1:$CTLA" -config "$TEST/a.json" -device-id-file "$TEST/device-a.id" >"$TEST/a.log" 2>&1 &
DA=$!
"$BUILD/vnetd" -ctl "127.0.0.1:$CTLB" -config "$TEST/b.json" -device-id-file "$TEST/device-b.id" >"$TEST/b.log" 2>&1 &
DB=$!

wait_ready() {
    local addr="$1" name="$2"
    for i in $(seq 1 30); do
        if "$BUILD/vnetctl" --ctl "$addr" status >/dev/null 2>&1; then
            echo "$name ready"
            return 0
        fi
        sleep 0.3
    done
    echo "$name NOT ready"
    cat "$TEST/${name: -1}.log"
    return 1
}
wait_ready "http://127.0.0.1:$CTLA" "daemon-A"
wait_ready "http://127.0.0.1:$CTLB" "daemon-B"

echo "== create (daemon A) =="
CREATE="$("$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLA" create --server "http://127.0.0.1:$SRV_PORT" --port 51820)"
echo "$CREATE"
NID="$(echo "$CREATE" | "$ROOT/scripts/jsonfield.py" networkId)"
CODE="$(echo "$CREATE" | "$ROOT/scripts/jsonfield.py" pairingCode)"
LINK="vnet://join?nid=$NID&code=$CODE"
echo "link: $LINK"

echo "== join (daemon B) =="
"$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLB" join --server "http://127.0.0.1:$SRV_PORT" --port 51821 --link "$LINK"

echo "== waiting for handshake (12s) =="
sleep 12

echo "== status A =="
STAT_A="$("$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLA" status)"
echo "$STAT_A"
echo "== status B =="
STAT_B="$("$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLB" status)"
echo "$STAT_B"

STAT_A="$(echo "$STAT_A" | "$ROOT/scripts/peerstats.py")"
STAT_B="$(echo "$STAT_B" | "$ROOT/scripts/peerstats.py")"
HS_A="$(echo "$STAT_A" | cut -d' ' -f1)"
HS_B="$(echo "$STAT_B" | cut -d' ' -f1)"
TX_A="$(echo "$STAT_A" | cut -d' ' -f2)"
RX_A="$(echo "$STAT_A" | cut -d' ' -f3)"

echo ""
echo "== handshake A=$HS_A B=$HS_B txA=$TX_A rxA=$RX_A =="
if [ -n "$HS_A" ] && [ "$HS_A" != "0" ] && [ -n "$HS_B" ] && [ "$HS_B" != "0" ] && [ -n "$TX_A" ] && [ "$TX_A" != "0" ]; then
    echo "E2E RESULT: PASS (tunnel established, data flowing)"
else
    echo "E2E RESULT: FAIL"
    echo "--- server log ---"; tail -5 "$TEST/server.log"
    echo "--- daemon A log ---"; cat "$TEST/a.log"
    echo "--- daemon B log ---"; cat "$TEST/b.log"
fi

echo ""
echo "== enforced-auth phase: server2 + daemon C (gate on) =="
CTLC=29434
SRV2_PORT=8102
"$BUILD/server" -addr "127.0.0.1:$SRV2_PORT" -probe-addr "127.0.0.1:8103" \
    -admin-token e2e-token -require-device-auth >"$TEST/server2.log" 2>&1 &
SRV2=$!
"$BUILD/vnetd" -ctl "127.0.0.1:$CTLC" -config "$TEST/c.json" -device-id-file "$TEST/device-c.id" >"$TEST/c.log" 2>&1 &
DC=$!
wait_ready "http://127.0.0.1:$CTLC" "daemon-C"

# unbound create must be refused with the enrollment hint
if "$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLC" create --server "http://127.0.0.1:$SRV2_PORT" --port 51822 \
    >/dev/null 2>"$TEST/c-gate.err"; then
    echo "FAIL: unbound create should be refused"
    exit 1
fi
grep -q "设备未授权" "$TEST/c-gate.err" || { echo "FAIL: gate message missing:"; cat "$TEST/c-gate.err"; exit 1; }
echo "gate: unbound create refused OK"

# admin generates one auth code
AUTH="$(curl -s -X POST "http://127.0.0.1:$SRV2_PORT/admin/devices/authcodes/generate" \
    -H "Authorization: Bearer e2e-token" -H "Content-Type: application/json" -d '{"count":1}')"
AUTHCODE="$(echo "$AUTH" | python3 -c 'import json,sys;print(json.load(sys.stdin)["codes"][0])')"
[ -n "$AUTHCODE" ] || { echo "FAIL: no auth code generated"; exit 1; }

# bind daemon C, then create must succeed; status reports bound
"$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLC" bind --server "http://127.0.0.1:$SRV2_PORT" --code "$AUTHCODE"
CREATEC="$("$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLC" create --server "http://127.0.0.1:$SRV2_PORT" --port 51822)"
NIDC="$(echo "$CREATEC" | "$ROOT/scripts/jsonfield.py" networkId)"
[ -n "$NIDC" ] || { echo "FAIL: create after bind"; exit 1; }
"$BUILD/vnetctl" --ctl "http://127.0.0.1:$CTLC" status | python3 -c 'import json,sys;d=json.load(sys.stdin);assert d.get("bound") is True, d' \
    || { echo "FAIL: status does not report bound"; exit 1; }
echo "auth: bind + create OK (network $NIDC)"

kill "$DC" "$SRV2" 2>/dev/null || true
echo "enforced-auth phase OK"

kill "$DA" "$DB" "$SRV" 2>/dev/null || true
echo "== done =="
