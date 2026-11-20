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

# Reuse the release keystore for local adb installs when the caller has not
# supplied one explicitly. CI injects the same variables from GitHub Secrets.
LOCAL_KEY_PROPS="$HOME/.android-keys/dschat-release.properties"
if [[ -z "${ANDROID_KEYSTORE_PATH:-}" && -f "$LOCAL_KEY_PROPS" ]]; then
  ANDROID_KEYSTORE_PATH="$(sed -n 's/^storeFile=//p' "$LOCAL_KEY_PROPS" | head -1)"
  ANDROID_KEYSTORE_PASSWORD="$(sed -n 's/^storePassword=//p' "$LOCAL_KEY_PROPS" | head -1)"
  ANDROID_KEY_ALIAS="$(sed -n 's/^keyAlias=//p' "$LOCAL_KEY_PROPS" | head -1)"
  ANDROID_KEY_PASSWORD="$(sed -n 's/^keyPassword=//p' "$LOCAL_KEY_PROPS" | head -1)"
  export ANDROID_KEYSTORE_PATH ANDROID_KEYSTORE_PASSWORD ANDROID_KEY_ALIAS ANDROID_KEY_PASSWORD
fi

gomobile bind -target=android -androidapi 26 -o app/android/app/libs/chatsvr.aar ./app/mobile

if [[ -n "${ANDROID_KEYSTORE_PATH:-}" ]]; then
  (cd app/android && ./gradlew --no-daemon assembleRelease)
  APK="dist/android/dschat-android-release.apk"
  cp app/android/app/build/outputs/apk/release/app-release.apk "$APK"
  APKSIGNER="$(find "$ANDROID_HOME/build-tools" -name apksigner -type f 2>/dev/null | sort -V | tail -1 || true)"
  if [[ -n "$APKSIGNER" ]]; then
    "$APKSIGNER" verify --print-certs "$APK" >/dev/null
  fi
  cp app/android/app/libs/chatsvr.aar dist/android/chatsvr.aar
  echo "built $APK (release-signed) and dist/android/chatsvr.aar"
else
  (cd app/android && ./gradlew --no-daemon assembleDebug)
  cp app/android/app/build/outputs/apk/debug/app-debug.apk dist/android/dschat-debug.apk
  cp app/android/app/libs/chatsvr.aar dist/android/chatsvr.aar
  echo "built dist/android/dschat-debug.apk (debug) and dist/android/chatsvr.aar"
fi
