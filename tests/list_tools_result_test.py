import json
import os
import subprocess

import pytest

pytestmark = pytest.mark.anyio


def send_jsonrpc(process, method, params=None, request_id=1):
    """Send a JSON-RPC request over stdin and read the response from stdout."""
    request = {"jsonrpc": "2.0", "id": request_id, "method": method}
    if params:
        request["params"] = params
    line = json.dumps(request) + "\n"
    process.stdin.write(line.encode())
    process.stdin.flush()

    raw = process.stdout.readline()
    return json.loads(raw)


@pytest.fixture
def grafana_env():
    env = {"GRAFANA_URL": os.environ.get("GRAFANA_URL", "http://localhost:3000")}
    if key := os.environ.get("GRAFANA_SERVICE_ACCOUNT_TOKEN"):
        env["GRAFANA_SERVICE_ACCOUNT_TOKEN"] = key
    elif key := os.environ.get("GRAFANA_API_KEY"):
        env["GRAFANA_API_KEY"] = key
    return env


async def test_list_tools_result_includes_required_fields(grafana_env):
    """Verify that tools/list responses include resultType, cacheScope, and ttlMs.

    The Python MCP SDK (mcp>=2.0.0) and fastmcp (>=4.0.0) require these fields
    regardless of protocol version. Their absence causes a ValidationError that
    silently drops all tools when mcp-grafana is proxied via a Python-based
    aggregator. See https://github.com/grafana/mcp-grafana/issues/1140.
    """
    binary = os.environ.get("MCP_GRAFANA_PATH", "../dist/mcp-grafana")
    proc = subprocess.Popen(
        [binary, "--enabled-tools", "search"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env={**os.environ, **grafana_env},
    )
    try:
        # Initialize (required before tools/list).
        init_resp = send_jsonrpc(proc, "initialize", {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "test", "version": "0"},
        })
        assert "error" not in init_resp, f"initialize failed: {init_resp}"

        # Acknowledge initialization.
        notification = json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n"
        proc.stdin.write(notification.encode())
        proc.stdin.flush()

        # List tools.
        resp = send_jsonrpc(proc, "tools/list", {}, request_id=2)
        assert "error" not in resp, f"tools/list failed: {resp}"

        result = resp["result"]
        assert "tools" in result

        # These three fields are required by MCP spec 2025-11 / Python SDK 2.x.
        assert "resultType" in result, "resultType missing from tools/list response"
        assert "cacheScope" in result, "cacheScope missing from tools/list response"
        assert "ttlMs" in result, "ttlMs missing from tools/list response"

        assert result["resultType"] == "complete"
        assert result["cacheScope"] == "private"
        assert isinstance(result["ttlMs"], int)
        assert result["ttlMs"] == 0
    finally:
        proc.terminate()
        proc.wait(timeout=5)
