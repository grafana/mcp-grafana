import pytest
import os
import asyncio
import gc
import base64
import secrets
import socket
import subprocess
import sys
import time
from dotenv import load_dotenv
from mcp.client.sse import sse_client
from mcp.client.stdio import stdio_client
from mcp.client.streamable_http import streamablehttp_client
from mcp import ClientSession, StdioServerParameters

load_dotenv()

DEFAULT_GRAFANA_URL = "http://localhost:3000"
DEFAULT_MCP_URL = "http://localhost:8000"
DEFAULT_MCP_TRANSPORT = "sse"

# litellm requires provider prefix for Claude models
# Claude Sonnet 4.5
models = ["gpt-4o", "anthropic/claude-sonnet-4-5-20250929"]

pytestmark = pytest.mark.anyio


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
    # Check for the new service account token environment variable first
    if key := os.environ.get("GRAFANA_SERVICE_ACCOUNT_TOKEN"):
        env["GRAFANA_SERVICE_ACCOUNT_TOKEN"] = key
    elif key := os.environ.get("GRAFANA_API_KEY"):
        env["GRAFANA_API_KEY"] = key
        import warnings

        warnings.warn(
            "GRAFANA_API_KEY is deprecated, please use GRAFANA_SERVICE_ACCOUNT_TOKEN instead. See https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana for details on creating service account tokens.",
            DeprecationWarning,
        )
    elif (username := os.environ.get("GRAFANA_USERNAME")) and (
        password := os.environ.get("GRAFANA_PASSWORD")
    ):
        env["GRAFANA_USERNAME"] = username
        env["GRAFANA_PASSWORD"] = password
    return env


@pytest.fixture
def grafana_headers():
    headers = {
        "X-Grafana-URL": os.environ.get("GRAFANA_URL", DEFAULT_GRAFANA_URL),
    }
    # Check for the new service account token environment variable first
    if key := os.environ.get("GRAFANA_SERVICE_ACCOUNT_TOKEN"):
        headers["X-Grafana-API-Key"] = key
    elif key := os.environ.get("GRAFANA_API_KEY"):
        headers["X-Grafana-API-Key"] = key
        import warnings

        warnings.warn(
            "GRAFANA_API_KEY is deprecated, please use GRAFANA_SERVICE_ACCOUNT_TOKEN instead. See https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana for details on creating service account tokens.",
            DeprecationWarning,
        )
    elif (username := os.environ.get("GRAFANA_USERNAME")) and (
        password := os.environ.get("GRAFANA_PASSWORD")
    ):
        credentials = f"{username}:{password}"
        headers["Authorization"] = (
            "Basic " + base64.b64encode(credentials.encode("utf-8")).decode()
        )
    return headers


@pytest.fixture
async def mcp_client(mcp_transport, mcp_url, grafana_env, grafana_headers):
    if mcp_transport == "stdio":
        enabled_tools = "search,datasource,incident,prometheus,loki,elasticsearch,influxdb,alerting,dashboard,folder,oncall,asserts,sift,pyroscope,navigation,proxied,annotations,rendering,admin,clickhouse,cloudwatch"
        params = StdioServerParameters(
            command=os.environ.get("MCP_GRAFANA_PATH", "../dist/mcp-grafana"),
            args=["--debug", "--log-level", "debug", "--enabled-tools", enabled_tools],
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


def _free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _exe(name: str) -> str:
    """Append .exe on Windows so Python's subprocess can find Go binaries."""
    if sys.platform == "win32" and not name.endswith(".exe"):
        return name + ".exe"
    return name


# Repo root (parent of tests/) so binary paths resolve regardless of pytest cwd.
_REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), os.pardir))


@pytest.fixture(scope="session")
def mock_oidc_factory():
    procs = []
    def _factory(subject: str = "alice"):
        port = _free_port()
        proc = subprocess.Popen(
            [_exe(os.path.join(_REPO_ROOT, "bin", "mock-oidc")), "-addr", f":{port}", "-sub", subject],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        procs.append(proc)
        for _ in range(50):
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                    break
            except OSError:
                time.sleep(0.1)
        return {"issuer": f"http://localhost:{port}", "client_id": "mcp", "subject": subject}
    yield _factory
    for p in procs:
        p.terminate()
        p.wait(timeout=5)


@pytest.fixture(scope="session")
def auth_mcp_factory(mock_oidc_factory):
    procs = []
    def _factory(subject: str = "alice"):
        idp = mock_oidc_factory(subject)
        port = _free_port()
        enc_key = secrets.token_bytes(32).hex()
        env = os.environ.copy()
        env["GRAFANA_URL"] = "http://localhost:3000"
        # The Makefile's `build` target outputs to dist/mcp-grafana; respect
        # the same MCP_GRAFANA_PATH env override the rest of the suite uses
        # (see line 108) so `make build && pytest tests/` works without
        # extra steps.
        binary = os.environ.get(
            "MCP_GRAFANA_PATH",
            os.path.join(_REPO_ROOT, "dist", "mcp-grafana"),
        )
        proc = subprocess.Popen([
            _exe(binary),
            "-t", "streamable-http",
            "-address", f":{port}",
            "--auth-mode", "oauth-oidc",
            "--public-url", f"http://localhost:{port}",
            "--allow-insecure-auth",
            "--token-encryption-key", enc_key,
            "--oidc-issuer-url", idp["issuer"],
            "--oidc-client-id", idp["client_id"],
            # Stateless mode: skips MCP session-id handshake, so the test can
            # POST tools/list directly with just the OAuth Bearer.
            "--disable-proxied",
            # Include admin tools so RBAC spot-checks (list_teams, list_users_by_org)
            # are actually registered and can be filtered by role.
            "--enabled-tools", "search,datasource,incident,prometheus,loki,alerting,dashboard,folder,oncall,asserts,sift,admin,pyroscope,navigation,annotations,rendering,plugin,api",
        ], env=env)
        procs.append(proc)
        for _ in range(50):
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                    break
            except OSError:
                time.sleep(0.1)
        return {"base": f"http://localhost:{port}"}
    yield _factory
    for p in procs:
        p.terminate()
        p.wait(timeout=5)


# Backwards-compatible single-instance fixtures used by the existing test.
@pytest.fixture(scope="session")
def mock_oidc(mock_oidc_factory):
    return mock_oidc_factory()


@pytest.fixture(scope="session")
def auth_mcp(auth_mcp_factory):
    return auth_mcp_factory()
