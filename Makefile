GO ?= /usr/local/go/bin/go
BINARY ?= bin/codex-bridge

.PHONY: tidy build test run-hub run-bridge

tidy:
	$(GO) mod tidy

build:
	$(GO) build -ldflags "-s -w" -o $(BINARY) .

test:
	$(GO) test ./...

run-hub:
	$(GO) run . hub

run-bridge:
	$(GO) run . bridge

