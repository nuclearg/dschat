.PHONY: build test web run-server

build:
	go build -o bin/dschat ./app/osx
	go build -o bin/sessionsvr ./sessionsvr

test:
	go test ./...

web:
	cd app/core/web/app && pnpm install && pnpm build && rm -rf ../dist && cp -R dist ../dist

run-server: build
	./bin/sessionsvr -addr :5218
