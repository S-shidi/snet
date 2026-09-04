#!/usr/bin/env python3
"""
SNET 跨组路由通知器
读取 .openwork/routes/matrix.yaml，根据 git diff 文件列表
输出应被通知的 BOT 列表（stdout JSON）。

用法:
    python3 .openwork/routes/notifier.py <changed_files...>
    # 或管道
    git diff --name-only HEAD~1 | python3 .openwork/routes/notifier.py
"""
import json
import os
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("ERROR: 需要 PyYAML: pip install pyyaml", file=sys.stderr)
    sys.exit(2)

ROOT = Path(__file__).resolve().parent.parent
MATRIX = ROOT / "routes" / "matrix.yaml"
LOG = ROOT / "logs" / "routes.log"

def load_matrix():
    with open(MATRIX) as f:
        return yaml.safe_load(f)

def match_route(file_path: str, routes: list) -> list:
    import fnmatch
    hits = []
    for r in routes:
        pattern = r["match"]
        if fnmatch.fnmatch(file_path, pattern):
            hits.append(r)
    return hits

def main():
    if len(sys.argv) > 1:
        files = [a for a in sys.argv[1:] if not a.startswith("-")]
    else:
        files = [l.strip() for l in sys.stdin if l.strip()]

    if not files:
        print(json.dumps({"routes": [], "bots": [], "files": []}))
        return

    matrix = load_matrix()
    routes = matrix.get("routes", [])
    bots_hit = {}  # bot -> {primary, cc, files}

    for f in files:
        hits = match_route(f, routes)
        for h in hits:
            primary = h.get("primary")
            cc_list = h.get("cc", [])
            if primary:
                bots_hit.setdefault(primary, {"primary_for": [], "cc_for": []})
                if f not in bots_hit[primary]["primary_for"]:
                    bots_hit[primary]["primary_for"].append(f)
            for cc in cc_list:
                bots_hit.setdefault(cc, {"primary_for": [], "cc_for": []})
                if f not in bots_hit[cc]["cc_for"]:
                    bots_hit[cc]["cc_for"].append(f)

    result = {
        "files": files,
        "bots": [
            {"bot": name, "primary_for": v["primary_for"], "cc_for": v["cc_for"]}
            for name, v in bots_hit.items()
        ],
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))

    # 写日志
    LOG.parent.mkdir(parents=True, exist_ok=True)
    with open(LOG, "a") as f:
        f.write(json.dumps(result, ensure_ascii=False) + "\n")

if __name__ == "__main__":
    main()
