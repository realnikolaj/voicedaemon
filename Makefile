.PHONY: build build-noapm build-silero test test-noapm test-silero lint lint-noapm lint-silero check check-noapm check-silero smoke

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

build-silero:
	CGO_ENABLED=1 go build -tags silero ./cmd/voicedaemon/

test-silero:
	CGO_ENABLED=1 go test -race -tags silero ./...

lint-silero:
	golangci-lint run --build-tags silero

check-silero: build-silero test-silero lint-silero
