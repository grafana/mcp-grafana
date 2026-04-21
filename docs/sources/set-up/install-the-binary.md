---
title: Install the binary
menuTitle: Install the binary
description: Download or build the Grafana MCP server binary.
keywords:
  - binary
  - Go
  - install
  - MCP
weight: 3
aliases: []
---

# Install the binary

Install the Grafana MCP server by downloading a release binary or building from source. This gives you a single executable in your `$PATH`.

## What you'll achieve

You have the `mcp-grafana` binary available so your MCP client can run it directly (typically in stdio mode).

## Before you begin

- For source build: a Go toolchain (see [Go install](https://go.dev/doc/install)).

## Download a release

1. Open the [releases page](https://github.com/grafana/mcp-grafana/releases) on GitHub.
2. Download the archive for your platform.
3. Extract the binary and place it in a directory that is in your `$PATH`.

## Build from source

If you have a Go toolchain installed you can also build and install it from source. Use `go install` and set `GOBIN` so the binary is installed where you want it (for example, a directory in your `$PATH`).

```bash
GOBIN="$HOME/go/bin" go install github.com/grafana/mcp-grafana/cmd/mcp-grafana@latest
```

Ensure `$GOBIN` (or `$HOME/go/bin`) is in your `$PATH`. Then add the server to your MCP client config with `"command": "mcp-grafana"` (or the full path if needed).

## Next steps

- [Configure authentication](../../configure/authentication/) for Grafana credentials.
- [Deploy with Helm](../deploy-with-helm/) if you run the server on Kubernetes.
