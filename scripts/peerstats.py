#!/usr/bin/env python3
"""Extract first peer's handshake/tx/rx from a daemon status JSON (stdin).

Handles both the legacy single-network shape (top-level peerStats) and the
v2 multi-network shape (networks[], each with its own peerStats).
"""
import json
import sys

data = json.load(sys.stdin)
nets = data.get("networks")
if nets:
    hs = 0
    tx = 0
    rx = 0
    for n in nets:
        for s in n.get("peerStats", {}).values():
            hs = max(hs, s.get("LastHandshakeSec", 0))
            tx += s.get("TxBytes", 0)
            rx += s.get("RxBytes", 0)
    print(f"{hs} {tx} {rx}")
    sys.exit(0)

stats = data.get("peerStats", {})
if not stats:
    print("none 0 0")
    sys.exit(0)
s = next(iter(stats.values()))
print(f"{s.get('LastHandshakeSec', 0)} {s.get('TxBytes', 0)} {s.get('RxBytes', 0)}")
