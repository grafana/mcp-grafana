"""
Deterministic end-to-end tests: call tools directly over the MCP protocol, with
no LLM in the loop, and assert on the data seeded by docker-compose.

The Go integration tests already cover tool logic by calling handlers directly.
These tests cover what only exists once the binary is running behind a real
transport: the tools/list payload, argument decoding, result serialization,
error signalling and auth passthrough.
"""

import asyncio
import json
import re

import pytest
from jsonschema import Draft202012Validator
from mcp import ClientSession

pytestmark = pytest.mark.anyio

DEMO_DASHBOARD_UID = "fe9gm6guyzi0wd"

# Anthropic and OpenAI both reject tool names outside this pattern.
TOOL_NAME_RE = re.compile(r"^[a-zA-Z0-9_-]{1,64}$")
# OpenAI rejects these keywords at the top level of a function's parameters.
TOP_LEVEL_FORBIDDEN = {"oneOf", "anyOf", "allOf", "not", "enum"}


async def call_json(client: ClientSession, name: str, args: dict):
    result = await client.call_tool(name, args)
    assert not result.is_error, f"{name} returned an error: {result.content}"
    return json.loads(result.content[0].text)


async def test_tool_schemas_are_accepted_by_llm_providers(mcp_client: ClientSession):
    tools = (await mcp_client.list_tools()).tools
    assert len(tools) > 20, f"suspiciously few tools: {[t.name for t in tools]}"
    names = [t.name for t in tools]
    assert len(names) == len(set(names)), "duplicate tool names"

    problems = []
    for tool in tools:
        schema = tool.input_schema
        if not TOOL_NAME_RE.match(tool.name):
            problems.append(f"{tool.name}: invalid name")
        if not (tool.description or "").strip():
            problems.append(f"{tool.name}: empty description")
        if schema.get("type") != "object":
            problems.append(f"{tool.name}: top-level type is {schema.get('type')!r}")
        if bad := TOP_LEVEL_FORBIDDEN & schema.keys():
            problems.append(f"{tool.name}: top-level {sorted(bad)}")
        try:
            Draft202012Validator.check_schema(schema)
        except Exception as e:
            problems.append(f"{tool.name}: invalid JSON Schema: {e}")
        for req in schema.get("required", []):
            if req not in schema.get("properties", {}):
                problems.append(f"{tool.name}: required {req!r} not in properties")
    assert not problems, "\n".join(problems)


async def test_list_datasources(mcp_client: ClientSession):
    datasources = await call_json(mcp_client, "list_datasources", {})
    if isinstance(datasources, dict):
        datasources = datasources.get("datasources", datasources)
    uids = {ds["uid"] for ds in datasources}
    assert {"prometheus", "loki", "tempo"} <= uids, uids


async def test_get_dashboard_by_uid(mcp_client: ClientSession):
    data = await call_json(mcp_client, "get_dashboard_by_uid", {"uid": DEMO_DASHBOARD_UID})
    assert data["dashboard"]["title"] == "Demo"


async def test_unknown_dashboard_is_a_tool_error_not_a_protocol_error(
    mcp_client: ClientSession,
):
    # A failing tool must come back as isError=true so the model can see it;
    # a JSON-RPC error would make the client raise instead.
    result = await mcp_client.call_tool("get_dashboard_by_uid", {"uid": "does-not-exist"})
    assert result.is_error


async def test_search_dashboards(mcp_client: ClientSession):
    hits = await call_json(mcp_client, "search_dashboards", {"query": "Demo"})
    assert DEMO_DASHBOARD_UID in {h["uid"] for h in hits["dashboards"]}


async def test_alert_rules_list_and_filter(mcp_client: ClientSession):
    rules = await call_json(mcp_client, "alerting_rules_read", {"operation": "list"})
    titles = {r["title"] for r in rules}
    assert {"Test Alert Rule 1", "Test Alert Rule 2"} <= titles

    filtered = await call_json(
        mcp_client,
        "alerting_rules_read",
        {"operation": "list", "label_selectors": ['{rule="first"}']},
    )
    assert {r["title"] for r in filtered} == {"Test Alert Rule 1"}


async def test_get_alert_rule(mcp_client: ClientSession):
    rule = await call_json(
        mcp_client, "alerting_rules_read", {"operation": "get", "rule_uid": "test_alert_rule_1"}
    )
    assert rule["title"] == "Test Alert Rule 1"


async def test_contact_points(mcp_client: ClientSession):
    points = await call_json(
        mcp_client, "alerting_manage_routing", {"operation": "get_contact_points"}
    )
    assert {"Email1", "Email2"} <= {p["name"] for p in points}


async def test_query_prometheus(mcp_client: ClientSession):
    data = await call_json(
        mcp_client,
        "query_prometheus",
        {"datasourceUid": "prometheus", "expr": "vector(42)", "queryType": "instant", "endTime": "now"},
    )
    assert "42" in json.dumps(data)


async def test_loki_label_names(mcp_client: ClientSession):
    # promtail ships the compose containers' logs asynchronously, so the label
    # can take a little while to appear after the stack starts.
    names: list = []
    for _ in range(30):
        names = await call_json(mcp_client, "list_loki_label_names", {"datasourceUid": "loki"})
        if "container" in names:
            return
        await asyncio.sleep(2)
    pytest.fail(f"'container' label never appeared in Loki; got {names}")


async def test_update_dashboard_roundtrip(mcp_client: ClientSession, grafana_http):
    dashboard = {"title": "protocol-test-dashboard", "panels": [], "schemaVersion": 39}
    created = await call_json(
        mcp_client, "update_dashboard", {"dashboard": dashboard, "folderUid": "", "overwrite": True}
    )
    uid = created["uid"]
    try:
        fetched = await call_json(mcp_client, "get_dashboard_by_uid", {"uid": uid})
        assert fetched["dashboard"]["title"] == "protocol-test-dashboard"
    finally:
        await grafana_http.delete(f"/api/dashboards/uid/{uid}")
