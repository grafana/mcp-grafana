.DEFAULT_GOAL := help

.PHONY: help
help: ## Print this help message.
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: build-image
build-image: ## Build the Docker image.
	docker build -t mcp-grafana:latest .

.PHONY: lint lint-jsonschema lint-jsonschema-fix
lint: lint-jsonschema ## Lint the Go code.
	go tool -modfile go.tools.mod golangci-lint run

lint-jsonschema: ## Lint for unescaped commas in jsonschema tags.
	go run ./cmd/linters/jsonschema --path .

lint-jsonschema-fix: ## Automatically fix unescaped commas in jsonschema tags.
	go run ./cmd/linters/jsonschema --path . --fix

.PHONY: test test-unit
test-unit: ## Run the unit tests (no external dependencies required).
	go test -v -tags unit ./...
test: test-unit

.PHONY: test-integration
test-integration: ## Run only the Docker-based integration tests (Requires docker containers to be up and running).
	go test -v -tags integration ./...

.PHONY: test-cloud
test-cloud: ## Run only the cloud-based tests (requires cloud Grafana instance and credentials).
	go test -v -tags cloud ./tools

.PHONY: run
run: ## Run the MCP server in stdio mode.
	go run ./cmd/mcp-grafana

.PHONY: run-sse
run-sse: ## Run the MCP server in SSE mode.
	go run ./cmd/mcp-grafana --transport sse
