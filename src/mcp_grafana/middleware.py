from dataclasses import dataclass

from mcp.server import FastMCP
from starlette.datastructures import Headers
from starlette.exceptions import HTTPException

from .client import GrafanaClient, grafana_client
from .settings import GrafanaSettings, grafana_settings


@dataclass
class GrafanaInfo:
    """
    Simple container for the Grafana URL and API key.
    """

    api_key: str
    url: str

    @classmethod
    def from_headers(cls, headers: Headers) -> "GrafanaInfo | None":
        if (url := headers.get("X-Grafana-URL")) is not None and (
            key := headers.get("X-Grafana-API-Key")
        ) is not None:
            return cls(api_key=key, url=url)
        return None


class GrafanaMiddleware:
    """
    Middleware that sets up Grafana info for the current request.

    Grafana info will be stored in the `grafana_info` contextvar, which can be
    used by tools/resources etc to access the Grafana configuration for the
    current request, if it was provided.

    This should be used as a context manager before handling the /sse request.
    """

    def __init__(self, request, fail_if_unset=True):
        self.request = request
        self.fail_if_unset = fail_if_unset
        self.settings_token = None
        self.client_token = None

    async def __aenter__(self):
        if (info := GrafanaInfo.from_headers(self.request.headers)) is not None:
            current_settings = grafana_settings.get()
            new_settings = GrafanaSettings(
                url=info.url,
                api_key=info.api_key,
                tools=current_settings.tools,
            )
            self.settings_token = grafana_settings.set(new_settings)
            self.client_token = grafana_client.set(
                GrafanaClient.from_settings(new_settings)
            )
        elif self.fail_if_unset:
            raise HTTPException(status_code=403, detail="No Grafana settings found.")

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        if self.settings_token is not None:
            grafana_settings.reset(self.settings_token)
        if self.client_token is not None:
            grafana_client.reset(self.client_token)


async def run_sse_async_with_middleware(self: FastMCP) -> None:
    """
    Run the server using SSE transport, with a middleware that extracts
    Grafana authentication information from the request headers.

    The vast majority of this code is the same as the original run_sse_async
    method (see https://github.com/modelcontextprotocol/python-sdk/blob/44c0004e6c69e336811bb6793b7176e1eda50015/src/mcp/server/fastmcp/server.py#L436-L468).
    """

    from mcp.server.sse import SseServerTransport
    from starlette.applications import Starlette
    from starlette.routing import Mount, Route
    import uvicorn

    sse = SseServerTransport("/messages/")

    async def handle_sse(request):
        async with GrafanaMiddleware(request):
            async with sse.connect_sse(
                request.scope, request.receive, request._send
            ) as streams:
                await self._mcp_server.run(
                    streams[0],
                    streams[1],
                    self._mcp_server.create_initialization_options(),
                )

    starlette_app = Starlette(
        debug=self.settings.debug,
        routes=[
            Route("/sse", endpoint=handle_sse),
            Mount("/messages/", app=sse.handle_post_message),
        ],
    )

    config = uvicorn.Config(
        starlette_app,
        host=self.settings.host,
        port=self.settings.port,
        log_level=self.settings.log_level.lower(),
    )
    server = uvicorn.Server(config)
    await server.serve()
