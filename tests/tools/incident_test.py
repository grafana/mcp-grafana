import json

from mcp_grafana.tools.incident import list_incidents, ListIncidentsArguments

from . import mark_cloud

# All tests in this file require a cloud Grafana instance and
# an asynchronous event loop.
pytestmark = mark_cloud


async def test_list_incidents():
    arguments = ListIncidentsArguments(
        limit=2,
        drill=False,
        status="active",
    )
    incidents_bytes = await list_incidents(arguments)
    incidents = json.loads(incidents_bytes)
    previews = incidents["incidentPreviews"]
    assert len(previews) <= 2
    for preview in previews:
        assert preview["status"] == "active"
        assert preview["isDrill"] is False
