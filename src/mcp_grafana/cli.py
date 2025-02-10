import enum
from types import MethodType

import typer

from . import mcp

app = typer.Typer()


class Transport(enum.StrEnum):
    stdio = "stdio"
    sse = "sse"


@app.command()
def run(transport: Transport = Transport.stdio, header_auth: bool = False):
    if transport == Transport.sse and header_auth:
        from .middleware import run_sse_async_with_middleware

        # Monkeypatch the run_sse_async method to inject a Grafana middleware.
        # This is a bit of a hack, but fastmcp doesn't have a way of adding
        # middleware. It's not unreasonable to do this really, since fastmcp
        # is just a thin wrapper around the low level mcp server.
        mcp.run_sse_async = MethodType(run_sse_async_with_middleware, mcp)

    mcp.run(transport.value)
