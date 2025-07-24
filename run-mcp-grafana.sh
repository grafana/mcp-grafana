#!/bin/bash

# MCP Grafana Runner Script
# This script ensures the binary is built and runs the MCP server

set -e  # Exit on any error

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "Building mcp-grafana..." >&2
go build -o mcp-grafana ./cmd/mcp-grafana

echo "Starting MCP Grafana server..." >&2
exec ./mcp-grafana "$@"