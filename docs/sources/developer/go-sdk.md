---
title: Go SDK (programmatic use)
menuTitle: Go SDK
description: Use the Grafana MCP server as a Go library to build custom MCP contexts and TLS configuration.
keywords:
  - Go
  - SDK
  - library
  - MCP
weight: 1
aliases: []
---

# Go SDK (programmatic use)

You can use the Grafana MCP server as a Go library to build custom MCP server contexts, for example with custom TLS or debug settings. The package is available on [pkg.go.dev](https://pkg.go.dev/github.com/grafana/mcp-grafana).

## What you'll achieve

You integrate the server into your own Go program, using `ComposedStdioContextFunc` and `GrafanaConfig` (and optionally `TLSConfig`) so the server runs with your chosen configuration.

## Before you begin

- A Go toolchain and the module in your project: `go get github.com/grafana/mcp-grafana`.

## Compose a stdio context with config

Create a `GrafanaConfig` with your Grafana URL, credentials (or token), and options such as `Debug`. For TLS to Grafana, set `TLSConfig` with `CertFile`, `KeyFile`, and `CAFile`. Then build the context function with `mcpgrafana.ComposedStdioContextFunc(grafanaConfig)` and pass it to your MCP server setup so the server uses this context when handling requests.

Example (structure only):

```go
grafanaConfig := mcpgrafana.GrafanaConfig{
    Debug: true,
    TLSConfig: &mcpgrafana.TLSConfig{
        CertFile: "/path/to/client.crt",
        KeyFile:  "/path/to/client.key",
        CAFile:   "/path/to/ca.crt",
    },
}
contextFunc := mcpgrafana.ComposedStdioContextFunc(grafanaConfig)
```

Refer to the [package documentation](https://pkg.go.dev/github.com/grafana/mcp-grafana) for the exact types and constructors.

## Next steps

- [Observability (metrics and tracing)](../observability-metrics-and-tracing/) for Prometheus and OTLP.
- [Client TLS (Grafana connection)](../../configure/client-tls-grafana-connection/) for TLS flag reference.
