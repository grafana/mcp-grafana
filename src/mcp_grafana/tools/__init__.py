from mcp.server import FastMCP

from . import search
from ..settings import grafana_settings


def add_tools(mcp: FastMCP):
    if grafana_settings.tools.search.enabled:
        search.add_tools(mcp)
