GO ?= /usr/local/go/bin/go
BINARY ?= bin/codex-bridge

.PHONY: tidy build test doc-lint run-hub run-bridge

tidy:
	$(GO) mod tidy

build:
	$(GO) build -ldflags "-s -w" -o $(BINARY) .

test:
	$(GO) test ./...

doc-lint:
	./scripts/check-docs.sh

run-hub:
	$(GO) run . hub

run-bridge:
	$(GO) run . bridge
