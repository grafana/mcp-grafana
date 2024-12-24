from mcp.server import FastMCP

from .client import grafana_client, SearchDashboardsArguments


async def search_dashboards(arguments: SearchDashboardsArguments) -> bytes:
    """
    Search dashboards in the Grafana instance.
    """
    return await grafana_client.search_dashboards(arguments)


def add_tools(mcp: FastMCP):
    mcp.add_tool(search_dashboards)
