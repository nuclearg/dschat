#!/usr/bin/env bash
# Build all distributable packages locally. CI uses the same targets in
# .github/workflows/release.yml.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=dist/release
rm -rf "$OUT"
mkdir -p "$OUT"

echo "==> macOS signed DMG (arm64)"
MACOS_ARCH=arm64 ./scripts/build-macos-dmg.sh

echo "==> Windows app (amd64)"
make app-windows
cp dist/dschat-windows-amd64.exe "$OUT/dschat-windows-amd64.exe"

echo "==> sessionsvr cross builds"
for target in darwin/arm64 linux/arm64 linux/amd64 windows/amd64; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="sessionsvr-${goos}-${goarch}"
  exe=""
  if [[ "$goos" == "windows" ]]; then
    exe=".exe"
  fi
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags="-s -w" -o "$OUT/$name$exe" ./sessionsvr
done

if command -v sha256sum >/dev/null; then
  (cd "$OUT" && sha256sum * > SHA256SUMS)
else
  (cd "$OUT" && shasum -a 256 * > SHA256SUMS)
fi

echo "==> $OUT"
ls -lh "$OUT"
