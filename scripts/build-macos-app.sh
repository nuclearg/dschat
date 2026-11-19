#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

APP=dist/Dschat.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

ARCH="${MACOS_ARCH:-universal}"
if [[ "$ARCH" == "arm64" ]]; then
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o "$APP/Contents/MacOS/dschat" ./app/osx
else
  TMPBIN="$(mktemp -d)"
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o "$TMPBIN/dschat-arm64" ./app/osx
  CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o "$TMPBIN/dschat-amd64" ./app/osx
  lipo -create "$TMPBIN/dschat-arm64" "$TMPBIN/dschat-amd64" -output "$APP/Contents/MacOS/dschat"
  rm -rf "$TMPBIN"
fi
cp packaging/macos/Info.plist "$APP/Contents/Info.plist"

ICONSET="$(mktemp -d)/icon.iconset"
mkdir -p "$ICONSET"
for spec in 16:16 32:16@2x 32:32 64:32@2x 128:128 256:128@2x 256:256 512:256@2x 512:512 1024:512@2x; do
  size="${spec%%:*}"; name="${spec##*:}"
  sips -z "$size" "$size" app/desktop/assets/appicon.png --out "$ICONSET/icon_${name}.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/icon.icns"
rm -rf "$(dirname "$ICONSET")"

echo "built $APP"
