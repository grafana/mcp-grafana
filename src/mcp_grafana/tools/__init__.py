from mcp.server import FastMCP

from . import datasources, incident, prometheus, search
from ..settings import grafana_settings


def add_tools(mcp: FastMCP):
    """
    Add all enabled tools to the MCP server.
    """
    if grafana_settings.tools.search.enabled:
        search.add_tools(mcp)
    if grafana_settings.tools.datasources.enabled:
        datasources.add_tools(mcp)
    if grafana_settings.tools.incident.enabled:
        incident.add_tools(mcp)
    if grafana_settings.tools.prometheus.enabled:
        prometheus.add_tools(mcp)
