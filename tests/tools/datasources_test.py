import json

from mcp_grafana.tools.datasources import (
    list_datasources,
    get_datasource_by_uid,
    get_datasource_by_name,
)

from . import mark_integration

# All tests in this file require a running Grafana instance and
# an asynchronous event loop.
pytestmark = mark_integration


async def test_list_datasources():
    datasources_bytes = await list_datasources()
    datasources = json.loads(datasources_bytes)
    assert len(datasources) == 1
    assert datasources[0]["name"] == "Robust Perception"
    assert datasources[0]["id"] == 1
    assert datasources[0]["uid"] == "robustperception"


async def test_get_datasource_by_uid():
    datasource_bytes = await get_datasource_by_uid("robustperception")
    datasource = json.loads(datasource_bytes)
    assert datasource["name"] == "Robust Perception"
    assert datasource["id"] == 1
    assert datasource["uid"] == "robustperception"


async def test_get_datasource_by_name():
    datasource_bytes = await get_datasource_by_name("Robust Perception")
    datasource = json.loads(datasource_bytes)
    assert datasource["name"] == "Robust Perception"
    assert datasource["id"] == 1
    assert datasource["uid"] == "robustperception"
