.PHONY: build test web run-server run app-macos app-macos-dmg app-windows shells android

build:
	go build -o bin/dschat ./app/osx
	go build -o bin/sessionsvr ./sessionsvr

test:
	go test ./...

web:
	cd app/core/web/app && pnpm install && pnpm build && rm -rf ../dist && cp -R dist ../dist

run-server: build
	./bin/sessionsvr -addr :5218

# Build the current OS package and launch it. Kill a previously running
# dschat first so a stale tray/app instance cannot survive the restart.
run: web
	@set -e; \
	case "$$(uname -s)" in \
	  Darwin) \
	    pkill -x dschat 2>/dev/null || true; \
	    for i in $$(seq 1 30); do pgrep -x dschat >/dev/null || break; sleep 0.1; done; \
	    if pgrep -x dschat >/dev/null; then echo "dschat is still running; refusing to launch a second instance" >&2; exit 1; fi; \
	    MACOS_ARCH="$$(uname -m)" OFFICIAL_BASE_URL="http://localhost:5219" ./scripts/build-macos-app.sh; \
	    open dist/Dschat.app ;; \
	  MINGW*|MSYS*|CYGWIN*) \
	    taskkill //IM dschat-windows-amd64.exe //F >/dev/null 2>&1 || true; \
	    taskkill //IM dschat.exe //F >/dev/null 2>&1 || true; \
	    OFFICIAL_BASE_URL="http://localhost:5219" ./scripts/build-windows-app.sh; \
	    cmd //c start "" "dist\\dschat-windows-amd64.exe" ;; \
	  *) \
	    echo "make run currently supports macOS and Windows" >&2; exit 1 ;; \
	esac

app-macos: web
	./scripts/build-macos-app.sh

app-macos-dmg:
	./scripts/build-macos-dmg.sh

app-windows: web
	./scripts/build-windows-app.sh

shells: app-macos app-windows

android:
	./scripts/build-android.sh
