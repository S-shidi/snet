#!/bin/bash
set -e

export JAVA_HOME=/opt/homebrew/opt/openjdk@17
export ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
export ANDROID_NDK_HOME=$ANDROID_HOME/ndk/23.1.7779620
export GOPROXY=https://goproxy.cn,direct
export PATH="$HOME/go/bin:$JAVA_HOME/bin:$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ANDROID="$ROOT/android"

cd "$ROOT"

echo "=== Building web assets (shared UI) ==="
bash scripts/build-web.sh

echo "=== Building snet.aar (gomobile) ==="
gomobile bind -target=android -o /tmp/snet.aar ./snetbind/

echo "=== Extracting classes.jar and native libs ==="
rm -rf /tmp/snet-aar-extract
mkdir -p /tmp/snet-aar-extract
cd /tmp/snet-aar-extract
unzip -q /tmp/snet.aar

cp classes.jar "$ANDROID/app/libs/snet-classes.jar"
rm -rf "$ANDROID/app/src/main/jniLibs"
mkdir -p "$ANDROID/app/src/main/jniLibs"
cp -r jni/* "$ANDROID/app/src/main/jniLibs/"

cd "$ANDROID"

echo "=== Building Android APK ==="
./gradlew assembleDebug --no-daemon

APK="$ROOT/android/app/build/outputs/apk/debug/app-debug.apk"
SIZE=$(ls -lh "$APK" | awk '{print $5}')
echo ""
echo "=== BUILD COMPLETE ==="
echo "APK: $APK ($SIZE)"
echo "Install: adb install -r $APK"