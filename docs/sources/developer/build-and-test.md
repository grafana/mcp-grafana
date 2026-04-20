---
title: Build, test, and lint
menuTitle: Build and test
description: Run the Grafana MCP server from source, execute tests, and lint the repository.
keywords:
  - Go
  - Makefile
  - tests
  - development
weight: 3
aliases: []
---

# Build, test, and lint

Contributions are welcome. Open an issue or pull request on [GitHub](https://github.com/grafana/mcp-grafana). This project is written in Go.

## What you'll achieve

You can run the server from source, execute the test suites, and run linters locally.

## Before you begin

- [Go](https://go.dev/doc/install) installed for your platform.
- Optional: Docker for integration tests.

## Run the server locally

**STDIO** (default for local development):

```bash
make run
```

**SSE:**

```bash
go run ./cmd/mcp-grafana --transport sse
```

## Build and run the Docker image

Build the image:

```bash
make build-image
```

Run in SSE mode (image default):

```bash
docker run -it --rm -p 8000:8000 mcp-grafana:latest
```

Run in stdio mode:

```bash
docker run -it --rm mcp-grafana:latest -t stdio
```

## Run tests

There are three types of tests available: unit tests, integration tests, and cloud tests.


**Unit tests** (no external dependencies required):

```bash
make test-unit
```

You can also run:

```bash
make test
```

**Integration tests** (requires Docker containers):

```bash
make test-integration
```

**Cloud tests** (requires Grafana Cloud instance and credentials):

```bash
make test-cloud
```

**Note**: Cloud tests are automatically configured in CI. For local development, you'll need to set up your own Grafana Cloud instance and credentials.

For more comprehensive integration tests, run Grafana on port 3000 (for example with Docker Compose):

```bash
docker-compose up -d
make test-all
```

If you add tools, add integration tests; existing tests are a good template.

## Run linters

```bash
make lint
```

The repository includes a custom linter for unescaped commas in `jsonschema` struct tags (`description` fields must be escaped with `\\,` to avoid truncation). Run only that linter:

```bash
make lint-jsonschema
```

Refer to the [JSONSchema linter README](https://github.com/grafana/mcp-grafana/blob/main/internal/linter/jsonschema/README.md) in the repository.

## Next steps

- [Go SDK](../go-sdk/)
- [Observability (metrics and tracing)](../observability-metrics-and-tracing/)
