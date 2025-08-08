.DEFAULT_GOAL := help

.PHONY: help
help: ## Print this help message.
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo ""
	@grep -E '^[a-zA-Z_0-9-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: build-image
build-image: ## Build the Docker image.
	docker build -t mcp-grafana:latest .

.PHONY: build
build: ## Build the binary.
	go build -o dist/mcp-grafana ./cmd/mcp-grafana

.PHONY: lint lint-jsonschema lint-jsonschema-fix
lint: lint-jsonschema ## Lint the Go code.
	golangci-lint run

lint-jsonschema: ## Lint for unescaped commas in jsonschema tags.
	go run ./cmd/linters/jsonschema --path .

lint-jsonschema-fix: ## Automatically fix unescaped commas in jsonschema tags.
	go run ./cmd/linters/jsonschema --path . --fix

.PHONY: test test-unit
test-unit: ## Run the unit tests (no external dependencies required).
	go test -v -tags unit ./...
test: test-unit

.PHONY: test-integration
test-integration: ## Run only the Docker-based integration tests (requires docker-compose services to be running, use `make run-test-services` to start them).
	go test -v -tags integration ./...

.PHONY: test-cloud
test-cloud: ## Run only the cloud-based tests (requires cloud Grafana instance and credentials).
ifeq ($(origin GRAFANA_API_KEY), undefined)
	$(error GRAFANA_API_KEY is not set. Please 'export GRAFANA_API_KEY=...' or use a tool like direnv to load it from .envrc)
endif
	GRAFANA_URL=https://mcptests.grafana-dev.net go test -v -count=1 -tags cloud ./tools

.PHONY: test-python-e2e
test-python-e2e: ## Run Python E2E tests (requires docker-compose services and SSE server to be running, use `make run-test-services` and `make run-sse` to start them).
	cd tests && uv sync --all-groups
	cd tests && uv run pytest

.PHONY: test-python-e2e-local
test-python-e2e-local: ## Run Python E2E tests excluding those requiring external API keys (claude model tests).
	cd tests && uv sync --all-groups
	cd tests && uv run pytest -k "not claude" --tb=short

.PHONY: run
run: ## Run the MCP server in stdio mode.
	go run ./cmd/mcp-grafana

.PHONY: run-sse
run-sse: ## Run the MCP server in SSE mode.
	go run ./cmd/mcp-grafana --transport sse --log-level debug --debug

PHONY: run-streamable-http
run-streamable-http: ## Run the MCP server in StreamableHTTP mode.
	go run ./cmd/mcp-grafana --transport streamable-http --log-level debug --debug

.PHONY: run-test-services
run-test-services: ## Run the docker-compose services required for the unit and integration tests.
	docker compose up -d --build

.PHONY: test-e2e-full
test-e2e-full: ## Run full E2E test workflow: start services, rebuild server, run tests.
	@echo "Starting full E2E test workflow..."
	@mkdir -p .tmp
	@echo "Ensuring Docker services are running..."
	$(MAKE) run-test-services
	@echo "Stopping any existing MCP server processes..."
	-pkill -f "mcp-grafana.*sse" || true
	@echo "Building fresh MCP server binary..."
	$(MAKE) build
	@echo "Starting MCP server in background..."
	@GRAFANA_URL=http://localhost:3000 ./dist/mcp-grafana --transport sse --log-level debug --debug > .tmp/server.log 2>&1 & echo $$! > .tmp/mcp-server.pid
	@sleep 5
	@echo "Running Python E2E tests..."
	@$(MAKE) test-python-e2e-local; \
	test_result=$$?; \
	echo "Cleaning up MCP server..."; \
	kill `cat .tmp/mcp-server.pid 2>/dev/null` 2>/dev/null || true; \
	rm -rf .tmp; \
	exit $$test_result

.PHONY: test-e2e-cleanup
test-e2e-cleanup: ## Clean up any leftover E2E test processes and files.
	@echo "Cleaning up any leftover E2E test processes and files..."
	-pkill -f "mcp-grafana.*sse" || true
	-rm -rf .tmp
	@echo "Cleanup complete."
