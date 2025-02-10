from mcp.server import FastMCP

from ..client import grafana_client


async def list_datasources() -> bytes:
    """
    List datasources in the Grafana instance.
    """
    return await grafana_client.get().list_datasources()


async def get_datasource_by_uid(uid: str) -> bytes:
    """
    Get a datasource by uid.
    """
    return await grafana_client.get().get_datasource(uid=uid)


async def get_datasource_by_name(name: str) -> bytes:
    """
    Get a datasource by name.
    """
    return await grafana_client.get().get_datasource(name=name)


def add_tools(mcp: FastMCP):
    mcp.add_tool(list_datasources)
    mcp.add_tool(get_datasource_by_uid)
    mcp.add_tool(get_datasource_by_name)
