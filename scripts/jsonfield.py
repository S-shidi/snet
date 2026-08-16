#!/usr/bin/env python3
"""Extract a top-level JSON field from stdin (used by e2e.sh)."""
import json
import sys

key = sys.argv[1]
data = json.load(sys.stdin)
value = data.get(key, "")
if isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)
