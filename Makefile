.PHONY: build build-noapm test test-noapm lint lint-noapm check check-noapm smoke

build:
	go build ./cmd/voicedaemon/

build-noapm:
	go build -tags noapm ./cmd/voicedaemon/

test:
	go test -race ./...

test-noapm:
	go test -race -tags noapm ./...

lint:
	golangci-lint run

lint-noapm:
	golangci-lint run --build-tags noapm

check: build test lint

check-noapm: build-noapm test-noapm lint-noapm

smoke: build-noapm
	bash scripts/smoke-test.sh
