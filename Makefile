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
