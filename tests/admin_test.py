"""
Admin tests using DeepEval MCP evaluation.

Assert which tool(s) were called (expected tool must be among calls; multiple allowed),
then evaluate output quality with GEval + MCPUseMetric (tool-use effectiveness).
"""
import pytest
from typing import Dict
from mcp import ClientSession
import aiohttp
import uuid
import os
from conftest import models, DEFAULT_GRAFANA_URL
from utils import assert_mcp_eval, run_llm_tool_loop


pytestmark = pytest.mark.anyio


@pytest.fixture
async def grafana_team():
    """Create a temporary test team and clean it up after the test is done."""
    # Generate a unique team name to avoid conflicts
    team_name = f"test-team-{uuid.uuid4().hex[:8]}"

    # Get Grafana URL and service account token from environment
    grafana_url = os.environ.get("GRAFANA_URL", DEFAULT_GRAFANA_URL)

    auth_header = None
    # Check for the new service account token environment variable first
    if api_key := os.environ.get("GRAFANA_SERVICE_ACCOUNT_TOKEN"):
        auth_header = {"Authorization": f"Bearer {api_key}"}
    elif api_key := os.environ.get("GRAFANA_API_KEY"):
        auth_header = {"Authorization": f"Bearer {api_key}"}
        import warnings

        warnings.warn(
            "GRAFANA_API_KEY is deprecated, please use GRAFANA_SERVICE_ACCOUNT_TOKEN instead. See https://grafana.com/docs/grafana/latest/administration/service-accounts/#add-a-token-to-a-service-account-in-grafana for details on creating service account tokens.",
            DeprecationWarning,
        )

    if not auth_header:
        pytest.skip("No authentication credentials available to create team")

    # Create the team using Grafana API
    team_id = None
    async with aiohttp.ClientSession() as session:
        create_url = f"{grafana_url}/api/teams"
        async with session.post(
            create_url,
            headers=auth_header,
            json={"name": team_name, "email": f"{team_name}@example.com"},
        ) as response:
            if response.status != 200:
                resp_text = await response.text()
                pytest.skip(f"Failed to create team: {resp_text}")
            resp_data = await response.json()
            team_id = resp_data.get("teamId")

    # Yield the team info for the test to use
    yield {"id": team_id, "name": team_name}

    # Clean up after the test
    if team_id:
        async with aiohttp.ClientSession() as session:
            delete_url = f"{grafana_url}/api/teams/{team_id}"
            async with session.delete(delete_url, headers=auth_header) as response:
                if response.status != 200:
                    resp_text = await response.text()
                    print(f"Warning: Failed to delete team: {resp_text}")


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
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
@pytest.mark.flaky(max_runs=3)
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

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        (
            "Does the response contain specific information about "
            "the teams in Grafana? "
            f"There should be a team named {team_name}."
        ),
        expected_tools="list_teams",
    )
