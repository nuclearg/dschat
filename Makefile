.PHONY: build test web run-server app-macos app-windows shells android

build:
	go build -o bin/dschat ./app/osx
	go build -o bin/sessionsvr ./sessionsvr

test:
	go test ./...

web:
	cd app/core/web/app && pnpm install && pnpm build && rm -rf ../dist && cp -R dist ../dist

run-server: build
	./bin/sessionsvr -addr :5218

app-macos:
	./scripts/build-macos-app.sh

app-windows:
	./scripts/build-windows-app.sh

shells: app-macos app-windows

android:
	./scripts/build-android.sh
