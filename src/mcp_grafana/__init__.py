import enum
from typing import Literal

import anyio
import uvicorn
from mcp.server import FastMCP

from .tools import add_tools


class Transport(enum.StrEnum):
    http = "http"
    stdio = "stdio"
    sse = "sse"


class GrafanaMCP(FastMCP):
    async def run_http_async(self) -> None:
        from starlette.applications import Starlette
        from starlette.routing import Mount

        from .transports.http import handle_message

        async def handle_http(scope, receive, send):
            if scope["type"] != "http":
                raise ValueError("Expected HTTP request")
            async with handle_message(scope, receive, send) as (
                read_stream,
                write_stream,
            ):
                await self._mcp_server.run(
                    read_stream,
                    write_stream,
                    self._mcp_server.create_initialization_options(),
                )

        starlette_app = Starlette(
            debug=self.settings.debug,
            routes=[Mount("/", app=handle_http)],
        )

        config = uvicorn.Config(
            starlette_app,
            host=self.settings.host,
            port=self.settings.port,
            log_level=self.settings.log_level.lower(),
        )
        server = uvicorn.Server(config)
        await server.serve()

    def run(self, transport: Literal["http", "stdio", "sse"] = "stdio") -> None:
        """Run the FastMCP server. Note this is a synchronous function.

        Args:
            transport: Transport protocol to use ("stdio" or "sse")
        """
        if transport not in Transport:
            raise ValueError(f"Unknown transport: {transport}")

        if transport == "stdio":
            anyio.run(self.run_stdio_async)
        elif transport == "sse":
            anyio.run(self.run_sse_async)
        else:
            anyio.run(self.run_http_async)


# Create an MCP server
mcp = GrafanaMCP("Grafana", log_level="DEBUG")
add_tools(mcp)
