import json

from mcp_grafana.tools.search import search_dashboards, SearchDashboardsArguments

from . import mark_integration

# All tests in this file require a running Grafana instance and
# an asynchronous event loop.
pytestmark = mark_integration


async def test_search_dashboards():
    result_bytes = await search_dashboards(SearchDashboardsArguments(query="Demo"))
    result = json.loads(result_bytes)
    assert len(result) == 1
    db = result[0]
    assert db["id"] == 1
    assert db["uid"] == "fe9gm6guyzi0wd"
    assert db["title"] == "Demo"
    assert db["uri"] == "db/demo"
    assert db["tags"] == ["demo"]
