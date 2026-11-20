#!/usr/bin/env bash
# Build, Developer ID-sign, and package the macOS arm64 app as a DMG.
#
# Signing uses the current user's login keychain, so this script is intended to
# run on the Mac that owns the Developer ID Application certificate.
#
# Optional notarization (requires an App Store Connect Team API key):
#   APPLE_API_KEY=/path/AuthKey_XXX.p8 \
#   APPLE_API_KEY_ID=XXX \
#   APPLE_API_ISSUER_ID=... \
#   ./scripts/build-macos-dmg.sh
set -euo pipefail
cd "$(dirname "$0")/.."

IDENTITY="${APPLE_SIGNING_IDENTITY:-Developer ID Application: ji li (98C4AQNKS9)}"
ARCH="${MACOS_ARCH:-arm64}"
APP="dist/Dschat.app"
DMG="dist/release/dschat-macos-${ARCH}.dmg"

if ! security find-identity -v -p codesigning | grep -F "\"$IDENTITY\"" >/dev/null; then
  echo "signing identity not found: $IDENTITY" >&2
  security find-identity -v -p codesigning >&2
  exit 1
fi

echo "==> build $ARCH app"
MACOS_ARCH="$ARCH" ./scripts/build-macos-app.sh

echo "==> sign app"
codesign --force --options runtime --timestamp \
  --sign "$IDENTITY" \
  --identifier com.nuclearg.dschat \
  "$APP"
codesign --verify --deep --strict --verbose=2 "$APP"

echo "==> build dmg"
mkdir -p dist/release
rm -f "$DMG"
if python3 -c 'import dmgbuild' >/dev/null 2>&1; then
  APP_ABS="$(cd "$(dirname "$APP")" && pwd)/$(basename "$APP")"
  BG_ABS="$(pwd)/packaging/macos/dmg-background.png"
  python3 -m dmgbuild \
    -s packaging/macos/dmg-settings.py \
    -Dapp="$APP_ABS" \
    -Dbackground="$BG_ABS" \
    -Dvolume_name=dschat \
    dschat "$DMG"
else
  echo "dmgbuild not found; using a plain DMG with an Applications symlink" >&2
  STAGE="$(mktemp -d)"
  trap 'rm -rf "$STAGE"' EXIT
  mkdir -p "$STAGE/dschat"
  cp -R "$APP" "$STAGE/dschat/"
  ln -s /Applications "$STAGE/dschat/Applications"
  hdiutil create -volname "dschat" -srcfolder "$STAGE/dschat" \
    -ov -format UDZO "$DMG" >/dev/null
fi

echo "==> sign dmg"
codesign --force --timestamp --sign "$IDENTITY" "$DMG"
codesign --verify --verbose=2 "$DMG"
hdiutil verify "$DMG" >/dev/null

if [[ -n "${APPLE_API_KEY:-}" && -n "${APPLE_API_KEY_ID:-}" && -n "${APPLE_API_ISSUER_ID:-}" ]]; then
  echo "==> notarize dmg"
  # S3 Transfer Acceleration is unreliable from mainland China; default to
  # the regional endpoint. Set NOTARY_S3_ACCELERATION=1 to opt back in.
  if [[ "${NOTARY_S3_ACCELERATION:-0}" == "1" ]]; then
    xcrun notarytool submit "$DMG" \
      --key "$APPLE_API_KEY" \
      --key-id "$APPLE_API_KEY_ID" \
      --issuer "$APPLE_API_ISSUER_ID" \
      --wait
  else
    xcrun notarytool submit "$DMG" \
      --key "$APPLE_API_KEY" \
      --key-id "$APPLE_API_KEY_ID" \
      --issuer "$APPLE_API_ISSUER_ID" \
      --no-s3-acceleration \
      --wait
  fi
  xcrun stapler staple "$DMG"
  xcrun stapler validate "$DMG"
else
  echo "NOTICE: notarization skipped (set APPLE_API_KEY, APPLE_API_KEY_ID, APPLE_API_ISSUER_ID to enable)"
fi

echo "built $DMG"
