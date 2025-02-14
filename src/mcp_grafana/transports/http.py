import logging
from contextlib import asynccontextmanager
from dataclasses import dataclass
from json import JSONDecodeError
from typing import Any, Tuple

import anyio
from anyio.streams.memory import MemoryObjectReceiveStream, MemoryObjectSendStream
import httpx
from mcp import types
from pydantic import ValidationError
from starlette.datastructures import Headers
from starlette.requests import Request
from starlette.responses import JSONResponse, PlainTextResponse, Response
from starlette.types import ASGIApp, Receive, Scope, Send

from ..client import GrafanaClient, grafana_client
from ..settings import GrafanaSettings, grafana_settings

logger = logging.getLogger(__name__)


ReadStream = MemoryObjectReceiveStream[types.JSONRPCMessage | Exception]
ReadStreamWriter = MemoryObjectSendStream[types.JSONRPCMessage | Exception]
WriteStream = MemoryObjectSendStream[types.JSONRPCMessage]
WriteStreamReader = MemoryObjectReceiveStream[types.JSONRPCMessage]


def make_streams() -> Tuple[
    ReadStream, ReadStreamWriter, WriteStream, WriteStreamReader
]:
    read_stream: MemoryObjectReceiveStream[types.JSONRPCMessage | Exception]
    read_stream_writer: MemoryObjectSendStream[types.JSONRPCMessage | Exception]

    write_stream: MemoryObjectSendStream[types.JSONRPCMessage]
    write_stream_reader: MemoryObjectReceiveStream[types.JSONRPCMessage]

    read_stream_writer, read_stream = anyio.create_memory_object_stream(0)
    write_stream, write_stream_reader = anyio.create_memory_object_stream(0)
    return read_stream, read_stream_writer, write_stream, write_stream_reader


async def initialize(
    read_stream_writer: ReadStreamWriter,
    write_stream_reader: WriteStreamReader,
):
    """
    Initialize the MCP server for this request.

    In a stateful transport (e.g. stdio or sse) the client would
    send an initialize request to the server, and the server would send
    an 'initialized' response back to the client.

    In the HTTP transport we're trying to be stateless, so we'll have to
    handle the initialization ourselves.

    This function handles that initialization by sending the required
    messages to the server and consuming the response.
    """
    # First construct the initialize request.
    initialize_request = types.InitializeRequest(
        method="initialize",
        params=types.InitializeRequestParams(
            protocolVersion=types.LATEST_PROTOCOL_VERSION,
            capabilities=types.ClientCapabilities(
                experimental=None,
                roots=None,
                sampling=None,
            ),
            # TODO: get the name and version from the package metadata.
            clientInfo=types.Implementation(name="mcp-grafana", version="0.1.2"),
        ),
    )
    initialize_request = types.JSONRPCRequest(
        jsonrpc="2.0",
        id=1,
        **initialize_request.model_dump(by_alias=True, mode="json"),
    )
    # Send it to the server.
    await read_stream_writer.send(types.JSONRPCMessage(initialize_request))
    # We can ignore the response since we're not sending it back to the client.
    await write_stream_reader.receive()

    # Next we need to notify the server that we're initialized.
    initialize_notification = types.JSONRPCNotification(
        jsonrpc="2.0",
        **types.ClientNotification(
            types.InitializedNotification(method="notifications/initialized"),
        ).model_dump(by_alias=True, mode="json"),
    )
    await read_stream_writer.send(types.JSONRPCMessage(initialize_notification))
    # Notifications don't have a response, so we don't need to await the
    # write stream reader.


@asynccontextmanager
async def handle_message(scope: Scope, receive: Receive, send: Send):
    """
    ASGI application for handling MCP messages using the stateless HTTP transport.

    This function is called for each incoming message. It creates a new
    stream for reading and writing messages, which will be used by the
    MCP server, and handles:

        - decoding the client message from JSON into internal types
        - validating the client message
        - initializing the MCP server, which must be done on every request
          (since this is a stateless transport)
        - sending the client message to the MCP server
        - receiving the server's response
        - encoding the server's response into JSON and sending it back to the client

    The returned read and write streams are intended to be passed to
    `mcp.server.lowlevel.Server.run()` as the `read_stream` and `write_stream`
    arguments.
    """
    read_stream, read_stream_writer, write_stream, write_stream_reader = make_streams()

    async def handle_post_message():
        try:
            request = Request(scope, receive)
            if request.method != "POST":
                response = Response("Method not allowed", status_code=405)
                await response(scope, receive, send)
                return
            if scope["path"] != "/mcp":
                response = Response("Not found", status_code=404)
                await response(scope, receive, send)
                return

            try:
                json = await request.json()
            except JSONDecodeError as err:
                logger.error(f"Failed to parse message: {err}")
                response = Response("Could not parse message", status_code=400)
                await response(scope, receive, send)
                return

            try:
                client_message = types.JSONRPCMessage.model_validate(json)
                logger.debug(f"Validated client message: {client_message}")
            except ValidationError as err:
                logger.error(f"Failed to validate message: {err}")
                response = Response(f"Invalid message: {err}", status_code=400)
                await response(scope, receive, send)
                return

            # As part of the MCP spec we need to initialize before we can handle
            # any other messages; the server has this assumption embedded in it.
            # In a stateful flow (e.g. stdio or sse transports) the client would
            # send an initialize request to the server, and the server would send
            # a response back to the client. In this case we're trying to be stateless,
            # so we'll handle the initialization ourselves, unless this happens to be
            # an initialization request.
            try:
                types.InitializeRequest.model_validate(
                    client_message.root.model_dump(
                        by_alias=True, mode="json", exclude_none=True
                    )
                )
                logger.debug("Skipping automatic initialization")
            except ValidationError:
                logger.debug("Automatically handling initialization")
                await initialize(read_stream_writer, write_stream_reader)

            # Alright, now we can send the client message.
            logger.debug("Sending client message")
            await read_stream_writer.send(client_message)

            if isinstance(client_message.root, types.JSONRPCNotification):
                # Notifications don't have a response, so we don't need to wait for one.
                response = PlainTextResponse("Accepted", status_code=202)
                await response(scope, receive, send)
                return

            # Wait for the server's response, and forward it to the client.
            server_message = await write_stream_reader.receive()
            obj = server_message.model_dump(
                by_alias=True, mode="json", exclude_none=True
            )
            response = JSONResponse(obj)
            await response(scope, receive, send)
        finally:
            await read_stream_writer.aclose()
            await write_stream_reader.aclose()

    async with anyio.create_task_group() as tg:
        tg.start_soon(handle_post_message)
        yield (read_stream, write_stream)


@asynccontextmanager
async def http_client(url: str, headers: dict[str, Any] | None = None):
    read_stream, read_stream_writer, write_stream, write_stream_reader = make_streams()

    async with anyio.create_task_group() as tg:
        try:

            async def http_rw():
                logger.debug("Waiting for request body")
                body = await write_stream_reader.receive()

                logger.debug(f"Connecting to HTTP endpoint: {url}")
                async with httpx.AsyncClient(headers=headers) as client:
                    response = await client.post(
                        url, content=body.model_dump_json(by_alias=True)
                    )
                    logger.debug(f"Received response: {response.status_code}")
                    message = types.JSONRPCMessage.model_validate_json(response.content)
                    await read_stream_writer.send(message)

            tg.start_soon(http_rw)
            try:
                yield read_stream, write_stream
            finally:
                tg.cancel_scope.cancel()
        finally:
            await read_stream_writer.aclose()
            await write_stream_reader.aclose()


@dataclass
class GrafanaInfo:
    """
    Simple container for the Grafana URL and API key.
    """

    url: str
    access_token: str | None
    id_token: str | None
    api_key: str | None

    @classmethod
    def from_headers(cls, headers: Headers) -> "GrafanaInfo | None":
        url = headers.get("X-Grafana-URL")
        if url is None:
            return None
        api_key = headers.get("X-Grafana-API-Key")
        access_token = headers.get("X-Access-Token")
        id_token = headers.get("X-Grafana-Id")
        return cls(
            url=url, api_key=api_key, access_token=access_token, id_token=id_token
        )


class GrafanaAuthMiddleware:
    """
    ASGI middleware that extracts authn and authz headers from incoming
    requests and updates the settings and client contextvars for
    the current request.
    """

    def __init__(self, app: ASGIApp):
        self.app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send):
        if scope["type"] == "http":
            request = Request(scope)
            if (info := GrafanaInfo.from_headers(request.headers)) is not None:
                current_settings = grafana_settings.get()
                new_settings = GrafanaSettings(
                    url=info.url,
                    api_key=info.api_key,
                    access_token=info.access_token,
                    id_token=info.id_token,
                    tools=current_settings.tools,
                )
                settings_token = grafana_settings.set(new_settings)
                client_token = grafana_client.set(
                    GrafanaClient.from_settings(new_settings)
                )
                try:
                    await self.app(scope, receive, send)
                finally:
                    grafana_settings.reset(settings_token)
                    grafana_client.reset(client_token)
            else:
                await self.app(scope, receive, send)

        else:
            await self.app(scope, receive, send)
