#!/usr/bin/env bash
set -euo pipefail

if [[ $# -eq 0 || -z $1 ]]; then
  echo "Usage: bash scripts/build-python-wheels.sh VERSION [go-to-wheel arguments...]" >&2
  exit 2
fi
VERSION=$1
shift
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The build tool uses modern Python; the bundled launcher supports Python 3.9+.
# TODO: switch to PyPI once --package-path and binary compression are released
# (https://github.com/simonw/go-to-wheel/pull/5 and /pull/7).
exec uvx --python 3.13 --from "go-to-wheel @ git+https://github.com/sd2k/go-to-wheel@e7a9fe44440039a9709c3deee2f9fa6cad0411fd" \
  go-to-wheel . \
  --package-path ./cmd/mcp-grafana \
  --name mcp-grafana \
  --version "$VERSION" \
  --set-version-var github.com/grafana/mcp-grafana/v2.version \
  --description "Grafana MCP server - interact with Grafana via the Model Context Protocol" \
  --url "https://github.com/grafana/mcp-grafana" \
  --license Apache-2.0 \
  --readme README.md \
  --requires-python '>=3.9' \
  "$@"
