from mcp.server import FastMCP

from . import datasources, incident, prometheus, search
from ..settings import grafana_settings


def add_tools(mcp: FastMCP):
    """
    Add all enabled tools to the MCP server.
    """
    settings = grafana_settings.get()
    if settings.tools.search.enabled:
        search.add_tools(mcp)
    if settings.tools.datasources.enabled:
        datasources.add_tools(mcp)
    if settings.tools.incident.enabled:
        incident.add_tools(mcp)
    if settings.tools.prometheus.enabled:
        prometheus.add_tools(mcp)
