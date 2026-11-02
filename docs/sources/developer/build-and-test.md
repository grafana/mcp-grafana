---
title: Build, test, and lint
menuTitle: Build and test
description: Run the Grafana MCP server from source, execute tests, and lint the repository.
keywords:
  - Go
  - mise
  - tests
  - development
weight: 3
aliases:
  - /docs/grafana-cloud/machine-learning/mcp/developer/build-and-test/
---

# Build, test, and lint

Contributions are welcome. Open an issue or pull request on [GitHub](https://github.com/grafana/mcp-grafana). This project is written in Go.

## What you'll achieve

You can run the server from source, execute the test suites, and run linters locally.

## Before you begin

- [mise](https://mise.jdx.dev/getting-started.html) installed for your platform. Run `mise install` in the repository to get the pinned Go and other tools.
- Optional: Docker for integration tests.

## Run the server locally

**STDIO** (default for local development):

```bash
mise run run
```

**SSE:**

```bash
go run ./cmd/mcp-grafana --transport sse
```

## Build and run the Docker image

Build the image:

```bash
mise run build-image
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
mise run test-unit
```

You can also run:

```bash
mise run test
```

**Integration tests** (requires Docker containers):

```bash
mise run test-integration
```

**Cloud tests** (requires Grafana Cloud instance and credentials):

```bash
mise run test-cloud
```

**Note**: Cloud tests are automatically configured in CI. For local development, you'll need to set up your own Grafana Cloud instance and credentials.

For the full integration suite, start the supporting services first (Grafana and its dependencies on port 3000):

```bash
mise run run-test-services
mise run test-integration
```

If you add tools, add integration tests; existing tests are a good template.

## Run linters

```bash
mise run lint
```

The repository includes a custom linter for unescaped commas in `jsonschema` struct tags (`description` fields must be escaped with `\\,` to avoid truncation). Run only that linter:

```bash
mise run lint-jsonschema
```

Refer to the [JSONSchema linter README](https://github.com/grafana/mcp-grafana/blob/main/internal/linter/jsonschema/README.md) in the repository.

## Next steps

- [Go SDK](../go-sdk/)
- [Observability (metrics and tracing)](../observability-metrics-and-tracing/)
