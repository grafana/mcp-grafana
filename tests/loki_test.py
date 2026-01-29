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
)
from deepeval import assert_test
from deepeval.metrics import MCPUseMetric, GEval
from deepeval.test_case import LLMTestCase, LLMTestCaseParams


pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_loki_logs_tool(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    prompt = (
        "Can you query the last 10 log lines from container 'mcp-grafana-grafana-1'? Give me the raw log lines."
    )

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

    while response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments) if tool_call.function.arguments else {}
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )
        response = await acompletion(model=model, messages=messages, tools=tools)

    final_content = (
        (response.choices[0].message.content or "")
        if response.choices
        else ""
    )

    # Require the Loki tool that fetches logs; LLM may discover datasource via
    # list_datasources, search_dashboards, or a known UID (e.g. loki-datasource).
    assert_expected_tools_called(tools_called, "query_loki_logs")
    test_case = LLMTestCase(input=prompt, actual_output=final_content, mcp_servers=[mcp_server], mcp_tools_called=tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    output_metric = GEval(
        name="OutputQuality",
        criteria=(
            "Does the response contain specific information that could only come from a Loki datasource? "
            "This could be actual log lines with timestamps, container names, or a summary that references "
            "specific log data. The response should show evidence of real data rather than generic statements."
        ),
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )

    assert_test(test_case, [mcp_metric, output_metric])


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_loki_container_labels(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    prompt = (
        "Can you list the values for the label 'container' for the last 10 minutes? "
        "Use any available Loki datasource."
    )

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

    while response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments) if tool_call.function.arguments else {}
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )
        response = await acompletion(model=model, messages=messages, tools=tools)

    final_content = (
        (response.choices[0].message.content or "")
        if response.choices
        else ""
    )

    test_case = LLMTestCase(input=prompt, actual_output=final_content, mcp_servers=[mcp_server], mcp_tools_called=tools_called)

    # LLMs often discover Loki via search_dashboards/get_dashboard_panel_queries first;
    # MCPUseMetric penalizes that (score ~0.5). Use threshold 0.5 so exploratory tool use still passes.
    mcp_metric = MCPUseMetric(threshold=0.5)
    output_metric = GEval(
        name="OutputQuality",
        criteria=(
            "Does the response provide a list of container names found in the logs? "
            "It should present the container names in a readable format and may include additional "
            "context about their usage."
        ),
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )

    assert_test(test_case, [mcp_metric, output_metric])
