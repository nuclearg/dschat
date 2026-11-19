#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

export ANDROID_HOME="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
if [[ -z "${ANDROID_NDK_HOME:-}" ]]; then
  ANDROID_NDK_HOME="$(ls -d "$ANDROID_HOME"/ndk/* 2>/dev/null | sort -V | tail -1 || true)"
  export ANDROID_NDK_HOME
fi
if [[ -z "${ANDROID_NDK_HOME:-}" ]]; then
  echo "Android NDK not found. Install it with: sdkmanager 'ndk;27.2.12479018'" >&2
  exit 1
fi
export PATH="$HOME/go/bin:$PATH"

if ! command -v gomobile >/dev/null || ! command -v gobind >/dev/null; then
  echo "gomobile/gobind not found. Run:" >&2
  echo "  go install golang.org/x/mobile/cmd/gomobile@latest" >&2
  echo "  go install golang.org/x/mobile/cmd/gobind@latest" >&2
  echo "  gomobile init" >&2
  exit 1
fi

mkdir -p app/android/app/libs dist/android
echo "sdk.dir=$ANDROID_HOME" > app/android/local.properties

gomobile bind -target=android -androidapi 26 -o app/android/app/libs/chatsvr.aar ./app/mobile
(cd app/android && ./gradlew --no-daemon assembleDebug)

cp app/android/app/build/outputs/apk/debug/app-debug.apk dist/android/dschat-debug.apk
cp app/android/app/libs/chatsvr.aar dist/android/chatsvr.aar
echo "built dist/android/dschat-debug.apk and dist/android/chatsvr.aar"
