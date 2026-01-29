"""
Admin tests using DeepEval MCP evaluation.

Assert which tool(s) were called (expected tool must be among calls; multiple allowed),
then evaluate output quality with GEval + MCPUseMetric (tool-use effectiveness).
"""
import json
import pytest
from typing import Dict
from litellm import Message, acompletion
from mcp import ClientSession
import aiohttp
import uuid
import os
from conftest import models, DEFAULT_GRAFANA_URL
from utils import get_converted_tools
from mcp_eval_utils import (
    MCP_EVAL_THRESHOLD,
    assert_expected_tools_called,
    make_mcp_server,
    call_tool_and_record,
    make_test_case,
)
from deepeval import assert_test
from deepeval.metrics import MCPUseMetric, GEval
from deepeval.test_case import LLMTestCaseParams


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
    """
    Test list_users_by_org using DeepEval MCP evaluation.
    Run LLM with MCP tools, record tool calls, then evaluate with MCPUseMetric + GEval.
    """
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you list all users who are members of the Grafana organization?"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]
    tools_called: list = []

    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    if response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments) if tool_call.function.arguments else {}
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )

    final_response = await acompletion(model=model, messages=messages, tools=tools)
    final_content = final_response.choices[0].message.content or ""

    assert_expected_tools_called(tools_called, "list_users_by_org")
    test_case = make_test_case(prompt, final_content, mcp_server, tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    output_metric = GEval(
        name="OutputQuality",
        criteria=(
            "Does the response contain specific information about organization users "
            "in Grafana, such as usernames, emails, or roles?"
        ),
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )

    assert_test(test_case, [mcp_metric, output_metric])


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
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    team_name = grafana_team["name"]
    prompt = "Can you list the teams in Grafana?"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]
    tools_called: list = []

    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    if response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments) if tool_call.function.arguments else {}
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )

    final_response = await acompletion(model=model, messages=messages, tools=tools)
    final_content = final_response.choices[0].message.content or ""

    assert_expected_tools_called(tools_called, "list_teams")
    test_case = make_test_case(prompt, final_content, mcp_server, tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    output_metric = GEval(
        name="OutputQuality",
        criteria=(
            "Does the response contain specific information about "
            "the teams in Grafana? "
            f"There should be a team named {team_name}."
        ),
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )

    assert_test(test_case, [mcp_metric, output_metric])
