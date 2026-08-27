#!/bin/bash
# build-web.sh — Unified web asset builder for all platforms
#
# Builds the shared TypeScript UI into platform-specific bundles:
#   - Android: android/src/android.ts  → android/app/src/main/assets/web/app.js
#   - Docker:  desktop/src/web.ts      → deploy/docker/web/app.js
#   - Desktop: handled by Vite via `npx tauri build`
#
# Also syncs index.html and styles.css to Android and Docker targets.
# Run this script whenever shared/web/ TypeScript sources change.

set -e
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SHARED="$ROOT/shared/web"
ANDROID_OUT="$ROOT/android/app/src/main/assets/web"
DOCKER_OUT="$ROOT/deploy/docker/web"

echo "=== Building web assets ==="

# Ensure esbuild is available
if ! command -v esbuild &>/dev/null; then
  echo "ERROR: esbuild not found. Install with: npm install -g esbuild" >&2
  exit 1
fi

# 1. Bundle Android (WebBridge adapter)
echo "[1/3] Bundling Android app.js from android/src/android.ts ..."
esbuild "$ROOT/android/src/android.ts" \
  --bundle --format=iife --target=es2022 --minify \
  --outfile="$ANDROID_OUT/app.js"
echo "      → $ANDROID_OUT/app.js ($(wc -c < "$ANDROID_OUT/app.js") bytes)"

# 2. Bundle Docker web (fetch adapter)
echo "[2/3] Bundling Docker app.js from desktop/src/web.ts ..."
esbuild "$ROOT/desktop/src/web.ts" \
  --bundle --format=iife --target=es2022 --minify \
  --outfile="$DOCKER_OUT/app.js"
echo "      → $DOCKER_OUT/app.js ($(wc -c < "$DOCKER_OUT/app.js") bytes)"

# 3. Sync index.html and styles.css to both targets
echo "[3/3] Syncing index.html + styles.css ..."
cp "$SHARED/index.html" "$ANDROID_OUT/index.html"
cp "$SHARED/styles.css" "$ANDROID_OUT/styles.css"
cp "$SHARED/index.html" "$DOCKER_OUT/index.html"
cp "$SHARED/styles.css" "$DOCKER_OUT/styles.css"
echo "      → Android + Docker in sync"

echo ""
echo "=== Done ==="
echo "Android: $ANDROID_OUT/"
echo "Docker:  $DOCKER_OUT/"
echo ""
echo "NOTE: Desktop (Tauri) UI is built separately via: cd desktop && npx tauri build"
