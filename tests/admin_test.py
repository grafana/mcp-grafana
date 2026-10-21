"""
Admin tests using DeepEval MCP evaluation.

Assert which tool(s) were called (expected tool must be among calls; multiple allowed),
then evaluate output quality with GEval + MCPUseMetric (tool-use effectiveness).
"""
import pytest
from typing import Dict
from mcp import ClientSession
import uuid
from conftest import models
from utils import assert_mcp_eval, assert_mentions, run_llm_tool_loop


pytestmark = pytest.mark.anyio


@pytest.fixture
async def grafana_team(grafana_http):
    """Create a temporary test team and clean it up after the test is done."""
    team_name = f"test-team-{uuid.uuid4().hex[:8]}"
    resp = await grafana_http.post(
        "/api/teams", json={"name": team_name, "email": f"{team_name}@example.com"}
    )
    resp.raise_for_status()
    team_id = resp.json()["teamId"]
    yield {"id": team_id, "name": team_name}
    await grafana_http.delete(f"/api/teams/{team_id}")


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_list_users_by_org(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = (
        "List all users in the current Grafana organization: I need the full list of "
        "organization members with their userid, email, and role."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain specific information about organization users "
        "in Grafana, such as usernames, emails, or roles?",
        expected_tools="list_users_by_org",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_list_teams(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
    grafana_team: Dict[str, str],
):
    """
    Test list_teams using DeepEval MCP evaluation.
    Asserts list_teams was called, evaluates tool usage (MCPUseMetric) and output quality (GEval).
    """
    team_name = grafana_team["name"]
    prompt = "Can you list the teams in Grafana?"
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mentions(final_content, team_name)
    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        expected_tools="list_teams",
    )
