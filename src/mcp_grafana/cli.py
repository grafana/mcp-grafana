import typer

from . import mcp, Transport

app = typer.Typer()


@app.command()
def run(transport: Transport = Transport.stdio):
    mcp.run(transport.value)
