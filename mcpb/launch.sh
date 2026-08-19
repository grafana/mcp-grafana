#!/bin/sh
# Selects the Linux binary matching this machine's CPU architecture.
#
# MCPB manifests can only vary the server command per OS, not per CPU
# architecture (https://github.com/modelcontextprotocol/mcpb/issues/10), so on
# Linux the manifest runs this script instead of a binary. macOS needs no
# equivalent: its binary is a universal Mach-O carrying both architectures.
#
# The manifest invokes this as an argument to /bin/sh rather than executing it
# directly, so it only needs to be readable. A host that drops execute bits when
# unpacking (https://github.com/modelcontextprotocol/mcpb/issues/294) would
# otherwise leave the whole Linux server unstartable, with no opportunity for
# the workaround below to run.
set -eu

# CDPATH would make `cd` resolve the relative path somewhere unexpected.
unset CDPATH

dir=$(cd -- "$(dirname -- "$0")/.." && pwd)

case "$(uname -m)" in
  x86_64 | amd64) bin="$dir/linux-x64/mcp-grafana" ;;
  aarch64 | arm64) bin="$dir/linux-arm64/mcp-grafana" ;;
  *)
    echo "mcp-grafana: unsupported CPU architecture $(uname -m)" >&2
    exit 1
    ;;
esac

if [ ! -f "$bin" ]; then
  echo "mcp-grafana: $bin is missing from this bundle" >&2
  exit 1
fi

# Some hosts drop the executable bit when unpacking a bundle
# (https://github.com/modelcontextprotocol/mcpb/issues/294), which would leave
# the binary unrunnable even though it is present.
[ -x "$bin" ] || chmod +x "$bin" 2>/dev/null || true

exec "$bin" "$@"
