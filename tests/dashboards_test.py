import json
import pytest
from litellm import Message, acompletion
from mcp import ClientSession

from conftest import models
from utils import (
    get_converted_tools,
    MCP_EVAL_THRESHOLD,
    assert_expected_tools_called,
    make_mcp_server,
    call_tool_and_record,
    run_llm_tool_loop,
)
from deepeval import assert_test
from deepeval.metrics import MCPUseMetric, GEval
from deepeval.test_case import LLMTestCase, LLMTestCaseParams


pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_dashboard_panel_queries_tool(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    dashboard_uid = "fe9gm6guyzi0wd"
    prompt = f"Can you list the panel queries for the dashboard with UID {dashboard_uid}?"

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
            if tool_name == "get_dashboard_panel_queries":
                assert args.get("uid") == dashboard_uid, (
                    f"Expected uid={dashboard_uid!r}, got {args.get('uid')!r}"
                )
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )

    final_response = await acompletion(model=model, messages=messages, tools=tools)
    final_content = final_response.choices[0].message.content or ""

    assert_expected_tools_called(tools_called, "get_dashboard_panel_queries")
    test_case = LLMTestCase(input=prompt, actual_output=final_content, mcp_servers=[mcp_server], mcp_tools_called=tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    output_metric = GEval(
        name="OutputQuality",
        criteria=(
            "Does the response contain specific information about panel queries and titles "
            "for the Grafana dashboard (e.g. at least one panel name and its query)? "
        ),
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )

    assert_test(test_case, [mcp_metric, output_metric])


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_dashboard_update_with_patch_operations(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    # Create a non-provisioned test dashboard by copying the demo dashboard
    demo_result = await mcp_client.call_tool("get_dashboard_by_uid", {"uid": "fe9gm6guyzi0wd"})
    demo_data = json.loads(demo_result.content[0].text)
    dashboard_json = demo_data["dashboard"].copy()

    if "uid" in dashboard_json:
        del dashboard_json["uid"]
    if "id" in dashboard_json:
        del dashboard_json["id"]

    title = "Test Dashboard"
    dashboard_json["title"] = title
    dashboard_json["tags"] = ["python-integration-test"]

    create_result = await mcp_client.call_tool(
        "update_dashboard",
        {"dashboard": dashboard_json, "folderUid": "", "overwrite": False},
    )
    create_data = json.loads(create_result.content[0].text)
    created_dashboard_uid = create_data["uid"]

    updated_title = "Updated Test Dashboard"
    prompt = (
        f"Update the title of the Test Dashboard to {updated_title}. "
        "Search for the dashboard by title first."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_expected_tools_called(
        tools_called, ["search_dashboards", "update_dashboard"]
    )
    test_case = LLMTestCase(input=prompt, actual_output=final_content, mcp_servers=[mcp_server], mcp_tools_called=tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    output_metric = GEval(
        name="OutputQuality",
        criteria=(
            "Does the response indicate the dashboard was found and its title was updated successfully?"
        ),
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )

    assert_test(test_case, [mcp_metric, output_metric])
