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

.PHONY: lint
lint: ## Lint the Go code.
	go tool -modfile go.tools.mod golangci-lint run

.PHONY: test
test: ## Run the Go unit tests.
	go test ./...

.PHONY: test-all
test-all: ## Run the Go unit and integration tests.
	go test -v -tags integration ./...

.PHONY: run
run: ## Run the MCP server in stdio mode.
	go run ./...

.PHONY: run-sse
run-sse: ## Run the MCP server in SSE mode.
	go run ./... --transport sse
