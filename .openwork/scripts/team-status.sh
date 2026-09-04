#!/usr/bin/env bash
# SNET 团队状态速查
# 用法: bash .openwork/scripts/team-status.sh
set -e
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OW="$ROOT/.openwork"

echo "═══════════════════════════════════════════════"
echo "  SNET 研发团队状态  ($(date '+%Y-%m-%d %H:%M:%S'))"
echo "═══════════════════════════════════════════════"

echo ""
echo "── Project ──"
echo "  Hermes Project: snet (p_7ca5404b)"
echo "  Path: $ROOT"
echo "  Branch: $(git -C "$ROOT" branch --show-current 2>/dev/null || echo 'detached')"
echo "  HEAD:   $(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo 'n/a')"

echo ""
echo "── BOTs (7) ──"
for f in "$OW"/bots/*.md; do
  [ -f "$f" ] || continue
  name=$(basename "$f" .md)
  echo "  • @snet-$name-bot  ($(wc -l < "$f" | tr -d ' ') lines)"
done

echo ""
echo "── Routes ──"
echo "  Matrix: $OW/routes/matrix.yaml"
echo "  Notifier: $OW/routes/notifier.py"
echo "  Test: $(git -C "$ROOT" diff --name-only HEAD~1 2>/dev/null | python3 "$OW/routes/notifier.py" 2>/dev/null | python3 -c 'import json,sys;d=json.load(sys.stdin);print(len(d.get("bots",[])),"BOTS hit")' 2>/dev/null || echo 'n/a (need ≥2 commits)')"

echo ""
echo "── Recent post-commit (last 5) ──"
tail -5 "$OW/logs/post-commit.log" 2>/dev/null || echo "  (no commits logged yet)"

echo ""
echo "── Files ──"
echo "  $OW/TEAM_PLAN.md       $(wc -l < "$OW/TEAM_PLAN.md" | tr -d ' ') lines"
echo "  $OW/routes/matrix.yaml $(wc -l < "$OW/routes/matrix.yaml" | tr -d ' ') lines"
echo "  $OW/hooks/post-commit  $(wc -l < "$OW/hooks/post-commit" | tr -d ' ') lines"
echo ""
echo "── Quick commands ──"
echo "  bash .openwork/scripts/team-status.sh       # this status"
echo "  bash .openwork/scripts/install-hooks.sh     # install git post-commit hook"
echo "  python3 .openwork/routes/notifier.py <file> # test route resolution"
echo "═══════════════════════════════════════════════"
