import pytest
import os
import asyncio
import gc
import base64
from dotenv import load_dotenv
from mcp.client.sse import sse_client
from mcp.client.stdio import stdio_client
from mcp.client.streamable_http import streamablehttp_client
from mcp import ClientSession, StdioServerParameters

load_dotenv()

DEFAULT_GRAFANA_URL = "http://localhost:3000"
DEFAULT_MCP_URL = "http://localhost:8000"
DEFAULT_MCP_TRANSPORT = "sse"

models = ["gpt-4o", "claude-3-5-sonnet-20240620"]


@pytest.fixture
def anyio_backend():
    return "asyncio"


@pytest.fixture(autouse=True)
async def cleanup_sessions():
    """Clean up any lingering HTTP sessions after each test."""
    yield
    # Force garbage collection to clean up any unclosed sessions
    gc.collect()
    # Give a brief moment for cleanup
    await asyncio.sleep(0.01)


@pytest.fixture
def mcp_transport():
    return os.environ.get("MCP_TRANSPORT", DEFAULT_MCP_TRANSPORT)


@pytest.fixture
def mcp_url():
    return os.environ.get("MCP_GRAFANA_URL", DEFAULT_MCP_URL)


@pytest.fixture
def grafana_env():
    env = {"GRAFANA_URL": os.environ.get("GRAFANA_URL", DEFAULT_GRAFANA_URL)}
    if key := os.environ.get("GRAFANA_API_KEY"):
        env["GRAFANA_API_KEY"] = key
    elif (username := os.environ.get("GRAFANA_USERNAME")) and (password := os.environ.get("GRAFANA_USERNAME")):
        env["GRAFANA_USERNAME"] = username
        env["GRAFANA_PASSWORD"] = password
    return env


@pytest.fixture
def grafana_headers():
    headers = {
        "X-Grafana-URL": os.environ.get("GRAFANA_URL", DEFAULT_GRAFANA_URL),
    }
    if key := os.environ.get("GRAFANA_API_KEY"):
        headers["X-Grafana-API-Key"] = key
    elif (username := os.environ.get("GRAFANA_USERNAME")) and (password := os.environ.get("GRAFANA_PASSWORD")):
        credentials = f"{username}:{password}"
        headers["Authorization"] = "Basic " + base64.b64encode(credentials.encode("utf-8")).decode()
    return headers


@pytest.fixture
async def mcp_client(mcp_transport, mcp_url, grafana_env, grafana_headers):
    if mcp_transport == "stdio":
        params = StdioServerParameters(
            command=os.environ.get("MCP_GRAFANA_PATH", "../dist/mcp-grafana"),
            args=["--debug", "--log-level", "debug"],
            env=grafana_env,
        )
        async with stdio_client(params) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                yield session
    elif mcp_transport == "sse":
        url = f"{mcp_url}/sse"
        async with sse_client(url, headers=grafana_headers) as (read, write):
            async with ClientSession(read, write) as session:
                await session.initialize()
                yield session
    elif mcp_transport == "streamable-http":
        # Use HTTP client for streamable-http transport
        url = f"{mcp_url}/mcp"
        async with streamablehttp_client(url, headers=grafana_headers) as (
            read,
            write,
            _,
        ):
            async with ClientSession(read, write) as session:
                await session.initialize()
                yield session
    else:
        raise ValueError(f"Unsupported transport: {mcp_transport}")
