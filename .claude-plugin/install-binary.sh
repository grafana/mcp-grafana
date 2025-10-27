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

# Version to download
VERSION="v0.7.6"
ARCHIVE_NAME="mcp-grafana_${OS}_${ARCH}.${EXT}"
DOWNLOAD_URL="https://github.com/grafana/mcp-grafana/releases/download/${VERSION}/${ARCHIVE_NAME}"

# Download and extract binary if not exists or version mismatch
VERSION_FILE="${PLUGIN_ROOT}/.mcp-grafana-version"
if [ ! -f "${BINARY_PATH}" ] || [ ! -f "${VERSION_FILE}" ] || [ "$(cat ${VERSION_FILE})" != "${VERSION}" ]; then
    echo "Downloading mcp-grafana ${VERSION} for ${OS}-${ARCH}..." >&2

    TEMP_DIR=$(mktemp -d)
    trap "rm -rf ${TEMP_DIR}" EXIT

    ARCHIVE_PATH="${TEMP_DIR}/${ARCHIVE_NAME}"
    curl -fsSL "${DOWNLOAD_URL}" -o "${ARCHIVE_PATH}"

    # Download and verify checksums
    CHECKSUMS_URL="https://github.com/grafana/mcp-grafana/releases/download/${VERSION}/checksums.txt"
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
