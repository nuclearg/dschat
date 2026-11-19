#!/usr/bin/env bash
# Build all distributable packages locally. CI uses the same targets in
# .github/workflows/release.yml.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=dist/release
rm -rf "$OUT"
mkdir -p "$OUT"

echo "==> macOS app (arm64)"
MACOS_ARCH=arm64 ./scripts/build-macos-app.sh
ditto -c -k --norsrc --noextattr --keepParent dist/Dschat.app "$OUT/dschat-macos-arm64.zip"

echo "==> Windows app (amd64)"
make app-windows
(cd dist && zip -qr "release/dschat-windows-amd64.zip" dschat-windows-amd64.exe)

echo "==> sessionsvr cross builds"
for target in darwin/arm64 linux/arm64 linux/amd64 windows/amd64; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="sessionsvr-${goos}-${goarch}"
  stage="$OUT/$name"
  mkdir -p "$stage"
  exe=""
  if [[ "$goos" == "windows" ]]; then
    exe=".exe"
  fi
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w" -o "$stage/sessionsvr$exe" ./sessionsvr
  cp sessionsvr/config.properties.example "$stage/config.properties.example"
  if [[ "$goos" == "windows" ]]; then
    (cd "$OUT" && zip -qr "$name.zip" "$name")
  else
    tar -C "$OUT" -czf "$OUT/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
done

if command -v sha256sum >/dev/null; then
  (cd "$OUT" && sha256sum *.zip *.tar.gz > SHA256SUMS)
else
  (cd "$OUT" && shasum -a 256 *.zip *.tar.gz > SHA256SUMS)
fi

echo "==> $OUT"
ls -lh "$OUT"
