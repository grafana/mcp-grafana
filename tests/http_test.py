import multiprocessing
import socket
import time
from typing import AsyncGenerator, Generator

import pytest
import uvicorn
from mcp.server.lowlevel.server import Server
from mcp.types import Tool
from mcp.client.session import ClientSession
from starlette.applications import Starlette
from starlette.requests import Request
from starlette.routing import Route

from mcp_grafana.transports.http import handle_message, http_client


@pytest.fixture
def server_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def server_url(server_port: int) -> str:
    return f"http://127.0.0.1:{server_port}"


# A test server implementation.
class ServerTest(Server):
    def __init__(self):
        super().__init__("test_server_for_http")


# Test fixtures
def make_server_app() -> Starlette:
    """Create test Starlette app with SSE transport"""
    server = ServerTest()

    @server.list_tools()
    async def handle_list_tools() -> list[Tool]:
        return [
            Tool(
                name="test_tool",
                description="A test tool",
                inputSchema={"type": "object", "properties": {}},
            ),
            Tool(
                name="test_tool2",
                description="A second test tool",
                inputSchema={"type": "object", "properties": {}},
            ),
        ]

    async def handle_http(request: Request):
        async with handle_message(request.scope, request.receive, request._send) as (
            read_stream,
            write_stream,
        ):
            await server.run(
                read_stream,
                write_stream,
                server.create_initialization_options(),
            )

    app = Starlette(routes=[Route("/mcp", endpoint=handle_http, methods=["POST"])])
    return app


def run_server(server_port: int) -> None:
    app = make_server_app()
    server = uvicorn.Server(
        config=uvicorn.Config(
            app=app, host="127.0.0.1", port=server_port, log_level="error"
        )
    )
    print(f"starting server on {server_port}")
    server.run()

    # Give server time to start
    while not server.started:
        print("waiting for server to start")
        time.sleep(0.5)


@pytest.fixture()
def server(server_port: int) -> Generator[None, None, None]:
    proc = multiprocessing.Process(
        target=run_server, kwargs={"server_port": server_port}, daemon=True
    )
    print("starting process")
    proc.start()

    # Wait for server to be running
    max_attempts = 20
    attempt = 0
    print("waiting for server to start")
    while attempt < max_attempts:
        try:
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                s.connect(("127.0.0.1", server_port))
                break
        except ConnectionRefusedError:
            time.sleep(0.1)
            attempt += 1
    else:
        raise RuntimeError(
            "Server failed to start after {} attempts".format(max_attempts)
        )

    yield

    print("killing server")
    # Signal the server to stop
    proc.kill()
    proc.join(timeout=2)
    if proc.is_alive():
        print("server process failed to terminate")


@pytest.fixture
async def http_client_session(
    server,
    server_url: str,
) -> AsyncGenerator[ClientSession, None]:
    async with http_client(url=server_url + "/mcp") as (read_stream, write_stream):
        async with ClientSession(read_stream, write_stream) as session:
            yield session


@pytest.mark.anyio
async def test_http_client_list_tools(
    http_client_session: ClientSession,
) -> None:
    session = http_client_session
    response = await session.list_tools()
    assert len(response.tools) == 2
    assert response.tools[0].name == "test_tool"
    assert response.tools[1].name == "test_tool2"
