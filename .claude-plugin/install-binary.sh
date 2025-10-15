#!/usr/bin/env bash
set -euo pipefail

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT}"
BINARY_PATH="${PLUGIN_ROOT}/mcp-grafana"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "${ARCH}" in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    *)
        echo "Unsupported architecture: ${ARCH}" >&2
        exit 1
        ;;
esac

case "${OS}" in
    darwin)
        OS="darwin"
        ;;
    linux)
        OS="linux"
        ;;
    *)
        echo "Unsupported OS: ${OS}" >&2
        exit 1
        ;;
esac

# Version to download
VERSION="v0.7.6"
DOWNLOAD_URL="https://github.com/grafana/mcp-grafana/releases/download/${VERSION}/mcp-grafana_${OS}_${ARCH}"

# Download binary if not exists or version mismatch
VERSION_FILE="${PLUGIN_ROOT}/.mcp-grafana-version"
if [ ! -f "${BINARY_PATH}" ] || [ ! -f "${VERSION_FILE}" ] || [ "$(cat ${VERSION_FILE})" != "${VERSION}" ]; then
    echo "Downloading mcp-grafana ${VERSION} for ${OS}-${ARCH}..." >&2
    curl -fsSL "${DOWNLOAD_URL}" -o "${BINARY_PATH}"
    chmod +x "${BINARY_PATH}"
    echo "${VERSION}" > "${VERSION_FILE}"
fi

# Execute the binary with all arguments
exec "${BINARY_PATH}" "$@"
