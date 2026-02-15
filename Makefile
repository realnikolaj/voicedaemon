.PHONY: build test lint check smoke

TAGS ?= noapm

build:
	go build -tags $(TAGS) ./cmd/voicedaemon/

test:
	go test -race -tags $(TAGS) ./...

lint:
	golangci-lint run

check: build test lint

smoke: build
	bash scripts/smoke-test.sh
