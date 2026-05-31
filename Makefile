GO ?= go
BINARY ?= bin/codex-bridge
PREFIX ?= /usr/local

.PHONY: help tidy frontend build build-all test doc-lint run-hub run-bridge install clean docker

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

tidy: ## Sync go.mod / go.sum
	$(GO) mod tidy

frontend: ## Build the web UI into internal/web/static (requires Node 20+)
	cd frontend && npm ci && npm run build

build: ## Build the Go binary (assumes frontend is already built)
	CGO_ENABLED=0 $(GO) build -ldflags "-s -w" -o $(BINARY) .

build-all: frontend build ## Build the frontend then the Go binary

test: ## Run the Go test suite
	$(GO) test ./...

doc-lint: ## Validate docs, env references, and code anchors
	./scripts/check-docs.sh

run-hub: ## Run the Hub from source
	$(GO) run . hub

run-bridge: ## Run the Bridge from source
	$(GO) run . bridge

install: build ## Install the binary to $(PREFIX)/bin (use sudo if needed)
	install -m 0755 $(BINARY) $(PREFIX)/bin/codex-bridge

docker: ## Build the container image (tag: codex-bridge:local)
	docker build -t codex-bridge:local .

clean: ## Remove build artifacts
	rm -rf bin
