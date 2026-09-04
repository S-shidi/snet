#!/usr/bin/env bash
# 安装 git post-commit 钩子到 .git/hooks/
set -e
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HOOK_SRC="$ROOT/.openwork/hooks/post-commit"
HOOK_DST="$ROOT/.git/hooks/post-commit"

if [ ! -f "$HOOK_SRC" ]; then
  echo "ERROR: missing $HOOK_SRC" >&2
  exit 1
fi

# 已存在则备份
if [ -f "$HOOK_DST" ] && [ ! -L "$HOOK_DST" ]; then
  cp "$HOOK_DST" "$HOOK_DST.bak.$(date +%s)"
  echo "existing hook backed up"
fi

cp "$HOOK_SRC" "$HOOK_DST"
chmod +x "$HOOK_DST"
echo "✅ post-commit hook installed: $HOOK_DST"
echo "   test: git commit --allow-empty -m 'test snet route' --quiet"
