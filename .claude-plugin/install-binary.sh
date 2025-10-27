#!/usr/bin/env bash
set -euo pipefail

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT}"
BINARY_PATH="${PLUGIN_ROOT}/mcp-grafana"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "${ARCH}" in
    x86_64)
        ARCH="x86_64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    i386|i686)
        ARCH="i386"
        ;;
    *)
        echo "Unsupported architecture: ${ARCH}" >&2
        exit 1
        ;;
esac

# Determine OS, archive extension, and binary name
case "${OS}" in
    darwin)
        OS="Darwin"
        EXT="tar.gz"
        BINARY_NAME="mcp-grafana"
        ;;
    linux)
        OS="Linux"
        EXT="tar.gz"
        BINARY_NAME="mcp-grafana"
        ;;
    mingw*|msys*|cygwin*)
        OS="Windows"
        EXT="zip"
        BINARY_NAME="mcp-grafana.exe"
        BINARY_PATH="${PLUGIN_ROOT}/mcp-grafana.exe"
        ;;
    *)
        echo "Unsupported OS: ${OS}" >&2
        exit 1
        ;;
esac

# Get latest version from GitHub API
LATEST_API_URL="https://api.github.com/repos/grafana/mcp-grafana/releases/latest"
if command -v jq >/dev/null 2>&1; then
    VERSION=$(curl -fsSL "${LATEST_API_URL}" | jq -r '.tag_name')
else
    VERSION=$(curl -fsSL "${LATEST_API_URL}" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/')
fi

if [ -z "${VERSION}" ] || [ "${VERSION}" = "null" ]; then
    echo "Error: Failed to fetch latest version from GitHub" >&2
    exit 1
fi

ARCHIVE_NAME="mcp-grafana_${OS}_${ARCH}.${EXT}"
# Use latest redirect for download URLs
DOWNLOAD_URL="https://github.com/grafana/mcp-grafana/releases/latest/download/${ARCHIVE_NAME}"

# Download and extract binary if not exists or version mismatch
VERSION_FILE="${PLUGIN_ROOT}/.mcp-grafana-version"
if [ ! -f "${BINARY_PATH}" ] || [ ! -f "${VERSION_FILE}" ] || [ "$(cat ${VERSION_FILE})" != "${VERSION}" ]; then
    echo "Downloading mcp-grafana ${VERSION} for ${OS}-${ARCH}..." >&2

    TEMP_DIR=$(mktemp -d)
    trap "rm -rf ${TEMP_DIR}" EXIT

    ARCHIVE_PATH="${TEMP_DIR}/${ARCHIVE_NAME}"
    curl -fsSL "${DOWNLOAD_URL}" -o "${ARCHIVE_PATH}"

    # Download and verify checksums
    VERSION_NUMBER="${VERSION#v}" # Remove 'v' prefix
    CHECKSUMS_URL="https://github.com/grafana/mcp-grafana/releases/download/${VERSION}/mcp-grafana_${VERSION_NUMBER}_checksums.txt"
    CHECKSUMS_PATH="${TEMP_DIR}/checksums.txt"
    curl -fsSL "${CHECKSUMS_URL}" -o "${CHECKSUMS_PATH}"

    # Verify checksum
    echo "Verifying checksum..." >&2
    cd "${TEMP_DIR}"
    if command -v sha256sum >/dev/null 2>&1; then
        grep "${ARCHIVE_NAME}" "${CHECKSUMS_PATH}" | sha256sum -c -
    elif command -v shasum >/dev/null 2>&1; then
        grep "${ARCHIVE_NAME}" "${CHECKSUMS_PATH}" | shasum -a 256 -c -
    else
        echo "Warning: Neither sha256sum nor shasum found, skipping checksum verification" >&2
    fi
    cd - >/dev/null

    # Extract archive
    if [ "${EXT}" = "tar.gz" ]; then
        tar -xzf "${ARCHIVE_PATH}" -C "${TEMP_DIR}"
    else
        unzip -q "${ARCHIVE_PATH}" -d "${TEMP_DIR}"
    fi

    # Move binary to plugin root
    mv "${TEMP_DIR}/${BINARY_NAME}" "${BINARY_PATH}"
    chmod +x "${BINARY_PATH}"
    echo "${VERSION}" > "${VERSION_FILE}"

    echo "Successfully installed mcp-grafana ${VERSION}" >&2
fi

# Execute the binary with all arguments
exec "${BINARY_PATH}" "$@"
