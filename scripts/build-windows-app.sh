#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

mkdir -p dist
x86_64-w64-mingw32-windres -I . -o app/win/rsrc_windows_amd64.syso app/win/rsrc.rc
CGO_ENABLED=1 \
CC=x86_64-w64-mingw32-gcc \
GOOS=windows GOARCH=amd64 \
  go build -trimpath -ldflags="-H=windowsgui" -o dist/dschat-windows-amd64.exe ./app/win
rm -f app/win/rsrc_windows_amd64.syso

echo "built dist/dschat-windows-amd64.exe"
