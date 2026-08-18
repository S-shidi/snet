#!/bin/bash
# Multi-network loopback e2e: server + two daemons, two networks on one
# server, leave/rejoin/remove/netinfo/kick lifecycle.
# Requires root (creates utun interfaces). Run: sudo scripts/e2e-multinet.sh
set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD=/tmp/snet-build
TEST=/tmp/snet-e2e
CTLA=19432
CTLB=19433
SRV_PORT=8090
mkdir -p "$BUILD" "$TEST"

echo "== verifying prebuilt binaries =="
for bin in server snetd snetctl; do
    if [ ! -x "$BUILD/$bin" ]; then
        echo "missing $BUILD/$bin — run 'go build -o $BUILD/... ' first (as normal user)"
        exit 2
    fi
done

pkill -f "snet-build/snetd" 2>/dev/null || true
pkill -f "snet-build/server" 2>/dev/null || true
sleep 0.5

"$BUILD/server" -addr "127.0.0.1:$SRV_PORT" -probe-addr "127.0.0.1:8091" >"$TEST/server.log" 2>&1 &
SRV=$!
"$BUILD/snetd" -ctl "127.0.0.1:$CTLA" -config "$TEST/a.json" >"$TEST/a.log" 2>&1 &
DA=$!
"$BUILD/snetd" -ctl "127.0.0.1:$CTLB" -config "$TEST/b.json" >"$TEST/b.log" 2>&1 &
DB=$!

wait_ready() {
    local addr="$1" name="$2"
    for i in $(seq 1 30); do
        if "$BUILD/snetctl" --ctl "$addr" status >/dev/null 2>&1; then
            echo "$name ready"
            return 0
        fi
        sleep 0.3
    done
    echo "$name NOT ready"
    return 1
}
wait_ready "http://127.0.0.1:$CTLA" "daemon-A"
wait_ready "http://127.0.0.1:$CTLB" "daemon-B"

jsonfield() { "$ROOT/scripts/jsonfield.py" "$1"; }

echo "== create net1 (daemon A, auto subnet) =="
CREATE="$("$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" create --server "http://127.0.0.1:$SRV_PORT" --port 51820 --name "家庭网络")"
echo "$CREATE"
NID1="$(echo "$CREATE" | jsonfield networkId)"
CODE1="$(echo "$CREATE" | jsonfield pairingCode)"
SUB1="$(echo "$CREATE" | jsonfield subnet)"
[ -n "$SUB1" ] || { echo "FAIL: no subnet assigned"; exit 1; }

echo "== create net2 (daemon A, explicit subnet) =="
CREATE2="$("$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" create --server "http://127.0.0.1:$SRV_PORT" --port 51820 --name "办公网络" --subnet "172.16.77.0/24")"
echo "$CREATE2"
NID2="$(echo "$CREATE2" | jsonfield networkId)"
CODE2="$(echo "$CREATE2" | jsonfield pairingCode)"
SUB2="$(echo "$CREATE2" | jsonfield subnet)"
[ "$SUB2" = "172.16.77.0/24" ] || { echo "FAIL: explicit subnet ignored ($SUB2)"; exit 1; }

echo "== daemon A has 2 networks =="
COUNT="$( "$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" networks | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))' )"
echo "networks count: $COUNT"

echo "== join net1 (daemon B, second network on same server) =="
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLB" join --server "http://127.0.0.1:$SRV_PORT" --port 51821 --link "snet://join?nid=$NID1&code=$CODE1"

echo "== netinfo net1 (owner A): expect 2 nodes =="
INFO="$("$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" netinfo --nid "$NID1")"
echo "$INFO"
NODES="$(echo "$INFO" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["nodes"]))')"
[ "$NODES" = "2" ] || { echo "FAIL: net1 nodes=$NODES, want 2"; exit 1; }

echo "== waiting for handshake on net1 (12s) =="
sleep 12

echo "== status A =="
STAT_A="$("$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" status)"
echo "$STAT_A"
echo "== status B =="
STAT_B="$("$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLB" status)"
echo "$STAT_B"

STAT_A="$(echo "$STAT_A" | "$ROOT/scripts/peerstats.py")"
STAT_B="$(echo "$STAT_B" | "$ROOT/scripts/peerstats.py")"
HS_A="$(echo "$STAT_A" | cut -d' ' -f1)"
HS_B="$(echo "$STAT_B" | cut -d' ' -f1)"
TX_A="$(echo "$STAT_A" | cut -d' ' -f2)"

echo ""
echo "== net1 handshake A=$HS_A B=$HS_B txA=$TX_A =="
[ -n "$HS_A" ] && [ "$HS_A" != "0" ] && [ -n "$HS_B" ] && [ "$HS_B" != "0" ] && [ -n "$TX_A" ] && [ "$TX_A" != "0" ] \
    || { echo "FAIL: no tunnel on net1"; tail -5 "$TEST/server.log"; cat "$TEST/a.log" "$TEST/b.log"; exit 1; }

echo "== leave net2 (daemon A), node kept =="
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" leave --nid "$NID2"
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" networks | python3 -c "import json,sys; ns=json.load(sys.stdin); n=[x for x in ns if x['networkId']=='$NID2'][0]; assert not n['active'], 'leave did not stop'"

echo "== rejoin net2 (daemon A) =="
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" rejoin --nid "$NID2"
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" networks | python3 -c "import json,sys; ns=json.load(sys.stdin); n=[x for x in ns if x['networkId']=='$NID2'][0]; assert n['active'], 'rejoin did not activate'"

echo "== reset-code + rename net1 =="
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" reset-code --nid "$NID1"
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" rename --nid "$NID1" --name "家庭网络2"

echo "== kick daemon B's node from net1 (owner A) =="
A_NODE="$(echo "$CREATE" | jsonfield nodeId)"
B_NODE="$(echo "$INFO" | python3 -c "import json,sys; ns=json.load(sys.stdin)['nodes']; print([n['id'] for n in ns if n['id']!='$A_NODE'][0])")"
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" kick --nid "$NID1" --node "$B_NODE"

echo "== remove net2 from daemon A =="
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" remove --nid "$NID2"
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" networks | python3 -c "import json,sys; ns=json.load(sys.stdin); assert all(n['networkId']!='$NID2' for n in ns), 'remove failed'"

echo "== remove net1 from daemon B, then owner A deletes it =="
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLB" remove --nid "$NID1"
"$BUILD/snetctl" --ctl "http://127.0.0.1:$CTLA" delete --nid "$NID1"

echo "E2E MULTINET RESULT: PASS"
kill "$DA" "$DB" "$SRV" 2>/dev/null || true
echo "== done =="
