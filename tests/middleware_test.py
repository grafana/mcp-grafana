import asyncio
import json
from types import MethodType
from typing import AsyncIterator

import anyio
import httpx
from mcp.server import FastMCP
from mcp.types import (
    LATEST_PROTOCOL_VERSION,
    CallToolResult,
    ClientCapabilities,
    ClientNotification,
    Implementation,
    InitializeRequest,
    InitializeRequestParams,
    InitializedNotification,
    JSONRPCNotification,
    JSONRPCRequest,
    JSONRPCResponse,
)
import pytest
from httpx_sse import aconnect_sse

from mcp_grafana.tools import add_tools
from mcp_grafana.middleware import run_sse_async_with_middleware

from pytest_httpserver import HTTPServer


@pytest.fixture
def mcp():
    mcp = FastMCP("grafana")
    add_tools(mcp)
    return mcp


class TestMiddleware:
    """
    Test that our injected starlette middleware extracts headers and
    overrides settings per-request, as expected.

    Also ensure that the contextvars do not leak across requests.
    """

    @pytest.mark.asyncio
    async def test_no_headers_provided(self, mcp: FastMCP):
        """
        Ensure that the middleware fails if no headers are provided.
        """

        # Monkeypatch the MCP server to use our middleware.
        mcp.run_sse_async = MethodType(run_sse_async_with_middleware, mcp)
        mcp.settings.host = "127.0.0.1"
        mcp.settings.port = 9500
        async with anyio.create_task_group() as tg:
            tg.start_soon(mcp.run_sse_async, name="mcp")
            # Wait for the server to start.
            await asyncio.sleep(0.1)
            client = httpx.AsyncClient(
                base_url=f"http://{mcp.settings.host}:{mcp.settings.port}"
            )
            resp = await client.get("/sse")
            assert resp.status_code == httpx.codes.FORBIDDEN
            tg.cancel_scope.cancel()

    @pytest.mark.asyncio
    async def test_multiple_requests(self, mcp: FastMCP):
        """
        Ensure that the contextvars do not leak across requests.

        We need to:
            1. Start a couple of mock Grafana servers
            2. Start our MCP server
            3. Send a request to the MCP server pointing to the first
               Grafana server (using the X-Grafana-Url header)
            4. Send a different request to the MCP server pointing to
               the second Grafana server (using the X-Grafana-Url header)
            5. Ensure that the right request goes to the right server
        """

        # Start a couple of mock Grafana servers.
        with HTTPServer(port=10000) as g1, HTTPServer(port=10001) as g2:
            # Set up some responses from those servers.

            g1.expect_oneshot_request("/api/datasources").respond_with_json([{"id": 1}])
            g1.expect_oneshot_request(
                "/api/plugins/grafana-incident-app/resources/api/IncidentsService.CreateIncident",
                method="POST",
                # TODO: add proper request body.
            ).respond_with_json({})  # TODO: add response body

            g2.expect_oneshot_request(
                "/api/datasources/proxy/uid/foo/api/v1/label/__name__/values"
            ).respond_with_json({
                "status": "success",
                "data": [
                    "metric1",
                    "metric2",
                ],
            })

            # Hardcode a port for the MCP server.
            mcp.settings.host = "127.0.0.1"
            mcp.settings.port = 10002

            # Create clients for each server.
            # Note these clients send requests to the MCP server, not the Grafana server.
            # The initial SSE request includes headers that tell the server which
            # Grafana server to send tool requests to.
            g1_client = httpx.AsyncClient(
                base_url=f"http://{mcp.settings.host}:{mcp.settings.port}",
            )
            g2_client = httpx.AsyncClient(
                base_url=f"http://{mcp.settings.host}:{mcp.settings.port}"
            )

            # Monkeypatch the MCP server to use our middleware.
            mcp.run_sse_async = MethodType(run_sse_async_with_middleware, mcp)

            async with anyio.create_task_group() as tg:
                tg.start_soon(mcp.run_sse_async, name="mcp")
                # Wait for the server to start.
                await asyncio.sleep(0.1)

                # Send SSE requests to the MCP server, one for each Grafana server.
                # We can access tool call results over the SSE stream.
                async with (
                    aconnect_sse(
                        g1_client,
                        "GET",
                        "/sse",
                        headers={
                            "X-Grafana-Url": f"http://{g1.host}:{g1.port}",
                            "X-Grafana-Api-Key": "abcd123",
                        },
                    ) as g1_source,
                    aconnect_sse(
                        g2_client,
                        "GET",
                        "/sse",
                        headers={
                            "X-Grafana-Url": f"http://{g2.host}:{g2.port}",
                            "X-Grafana-Api-Key": "efgh456",
                        },
                    ) as g2_source,
                ):
                    g1_iter = g1_source.aiter_sse()
                    g2_iter = g2_source.aiter_sse()
                    # The URL to use is in the first SSE message.
                    g1_url = (await g1_iter.__anext__()).data
                    g2_url = (await g2_iter.__anext__()).data

                    # The MCP protocol requires us to send an initialize request
                    # before we can send any other requests.
                    await initialize(g1_client, g1_url, g1_iter)
                    await initialize(g2_client, g2_url, g2_iter)

                    # Send a tool call request using the first URL.
                    await g1_client.post(
                        g1_url,
                        json={
                            "jsonrpc": "2.0",
                            "id": 2,
                            "method": "tools/call",
                            "params": {"name": "list_datasources"},
                        },
                    )
                    result = await jsonrpc_result(g1_iter)
                    # This must have come from the first Grafana server.
                    assert json.loads(result.content[0].text) == json.dumps(  # type: ignore
                        [{"id": 1}], indent=4
                    )

                    # Send a tool call request using the second URL.
                    await g2_client.post(
                        g2_url,
                        json={
                            "jsonrpc": "2.0",
                            "id": 2,
                            "method": "tools/call",
                            "params": {
                                "name": "list_prometheus_metric_names",
                                "arguments": {"datasource_uid": "foo", "regex": ".*"},
                            },
                        },
                    )
                    result = await jsonrpc_result(g2_iter)
                    metrics = [x.text for x in result.content]  # type: ignore
                    # This must have come from the second Grafana server.
                    assert metrics == ["metric1", "metric2"]

                # As ridiculous as it sounds, there is no way to stop the uvicorn
                # server other than raising a signal (sigint or sigterm), which would
                # also cause the test to fail. Instead, we just cancel the task group
                # and let the test finish.
                # The annoying part of this is that there are tons of extra logs emitted
                # by uvicorn which can't be captured by pytest...
                tg.cancel_scope.cancel()


async def initialize(client: httpx.AsyncClient, url: str, stream: AsyncIterator):
    """
    Handle the initialization handshake with the MCP server.
    """
    req = InitializeRequest(
        method="initialize",
        params=InitializeRequestParams(
            protocolVersion=LATEST_PROTOCOL_VERSION,
            capabilities=ClientCapabilities(
                sampling=None,
                experimental=None,
            ),
            clientInfo=Implementation(name="mcp-grafana", version="0.1.2"),
        ),
    )
    jdoc = JSONRPCRequest(
        jsonrpc="2.0",
        id=1,
        **req.model_dump(by_alias=True, mode="json"),
    )
    resp = await client.post(url, json=jdoc.model_dump(by_alias=True))
    resp.raise_for_status()

    req = ClientNotification(
        InitializedNotification(method="notifications/initialized")
    )
    jdoc = JSONRPCNotification(
        jsonrpc="2.0",
        **req.model_dump(by_alias=True, mode="json"),
    )
    await client.post(url, json=jdoc.model_dump(by_alias=True))

    # Consume the stream to ensure that the initialization handshake
    # is complete.
    sse = await stream.__anext__()
    data = json.loads(sse.data)
    assert "result" in data


async def jsonrpc_result(stream: AsyncIterator) -> CallToolResult:
    """
    Extract the result of a 'call tool' JSONRPC request from the SSE stream.
    """
    jdoc = (await stream.__anext__()).data
    resp = JSONRPCResponse.model_validate_json(jdoc)
    return CallToolResult.model_validate(resp.result)
