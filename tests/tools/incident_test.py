import json

from mcp_grafana.tools.incident import list_incidents, ListIncidentsArguments

from . import mark_cloud

pytestmark = mark_cloud


async def test_list_incidents():
    arguments = ListIncidentsArguments(
        limit=2,
        drill=False,
        status="active",
    )
    incidents_bytes = await list_incidents(arguments)
    incidents = json.loads(incidents_bytes)
    assert len(incidents) <= 2
