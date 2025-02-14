from mcp.server import FastMCP

from ..client import SearchDashboardsArguments, grafana_client


async def search_dashboards(arguments: SearchDashboardsArguments) -> bytes:
    """
    Search dashboards in the Grafana instance.
    """
    return await grafana_client.get().search_dashboards(arguments)


def add_tools(mcp: FastMCP):
    mcp.add_tool(search_dashboards)
