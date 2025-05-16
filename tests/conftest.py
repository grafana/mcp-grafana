import pytest
import os
from dotenv import load_dotenv
from mcp.client.sse import sse_client
from mcp import ClientSession

load_dotenv()

DEFAULT_GRAFANA_URL = "http://localhost:3000"
DEFAULT_MCP_URL = "http://localhost:8000/sse"

models = ["gpt-4o", "claude-3-5-sonnet-20240620"]

@pytest.fixture
def anyio_backend():
    return "asyncio"


@pytest.fixture
def mcp_url():
    return os.environ.get("MCP_GRAFANA_URL", DEFAULT_MCP_URL)

@pytest.fixture
def grafana_headers():
    headers = {
        "X-Grafana-URL": os.environ.get("GRAFANA_URL", DEFAULT_GRAFANA_URL),
    }
    if key := os.environ.get("GRAFANA_API_KEY"):
        headers["X-Grafana-API-Key"] = key
    return headers

@pytest.fixture
async def mcp_client(mcp_url, grafana_headers):
    async with sse_client(mcp_url, headers=grafana_headers) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            yield session

@pytest.fixture
def mcp_url():
    return os.environ.get("MCP_GRAFANA_URL", DEFAULT_MCP_URL)

@pytest.fixture
async def mcp_client(mcp_url, grafana_headers):
    async with sse_client(mcp_url, headers=grafana_headers) as (
        read,
        write,
    ):
        async with ClientSession(read, write) as session:
            await session.initialize()
            yield session