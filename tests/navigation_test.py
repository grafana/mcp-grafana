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


async def _run_deeplink_test_with_expected_args(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
    prompt: str,
    criteria: str,
    expected_tool_args: dict,
    url_assert: tuple[str, str] | list[tuple[str, str]] | None = None,
):
    """
    Same flow as previous version: use llm_tool_call_sequence to force/validate
    that the LLM calls generate_deeplink with the given args, then get final content.
    Record tools_called by running the tool-call loop ourselves so we can feed DeepEval.
    """
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]
    tools_called: list = []

    # One round: LLM -> must call generate_deeplink with expected_tool_args (validated in assert_and_handle_tool_call)
    response = await acompletion(model=model, messages=messages, tools=tools)

    if response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments) if tool_call.function.arguments else {}
            # Validate expected args (same as previous version's llm_tool_call_sequence)
            for key, expected_value in expected_tool_args.items():
                assert key in args, f"Expected parameter '{key}' in tool arguments, got: {args}"
                if expected_value is not None:
                    actual = args[key]
                    if isinstance(expected_value, dict) and isinstance(actual, dict):
                        for k, v in expected_value.items():
                            assert k in actual and actual[k] == v, (
                                f"Expected {key}.{k}={v!r}, got {actual.get(k)!r}"
                            )
                    else:
                        assert actual == expected_value, (
                            f"Expected {key}={expected_value!r}, got {key}={actual!r}"
                        )
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )
    else:
        # LLM didn't call the tool - fail like previous version
        actual = response.choices[0].message.content if response.choices else ""
        raise AssertionError(
            f"Expected LLM to call generate_deeplink with args {expected_tool_args}. "
            f"No tool calls. Content: {actual[:200]}..."
        )

    final_response = await acompletion(model=model, messages=messages, tools=tools)
    final_content = final_response.choices[0].message.content or ""

    if url_assert:
        pairs = [url_assert] if isinstance(url_assert, tuple) else url_assert
        for substring, desc in pairs:
            assert substring in final_content, f"Expected {desc}, got: {final_content}"

    assert_expected_tools_called(tools_called, "generate_deeplink")
    test_case = LLMTestCase(input=prompt, actual_output=final_content, mcp_servers=[mcp_server], mcp_tools_called=tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    output_metric = GEval(
        name="OutputQuality",
        criteria=criteria,
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )
    assert_test(test_case, [mcp_metric, output_metric])


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_dashboard_deeplink(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Please create a dashboard deeplink for dashboard with UID 'test-uid'."
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with /d/ path and the dashboard UID?",
        expected_tool_args={"resourceType": "dashboard", "dashboardUid": "test-uid"},
        url_assert=("/d/test-uid", "dashboard URL with /d/test-uid"),
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_panel_deeplink(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Generate a deeplink for panel 5 in dashboard with UID 'test-uid'"
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with viewPanel parameter?",
        expected_tool_args={
            "resourceType": "panel",
            "dashboardUid": "test-uid",
            "panelId": 5,
        },
        url_assert=("viewPanel=5", "panel URL with viewPanel=5"),
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_explore_deeplink(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Generate a deeplink for Grafana Explore with datasource 'test-uid'"
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with /explore path?",
        expected_tool_args={"resourceType": "explore", "datasourceUid": "test-uid"},
        url_assert=("/explore", "explore URL with /explore path"),
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_time_range(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Generate a dashboard deeplink for 'test-uid' showing the last 6 hours"
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with time range parameters?",
        expected_tool_args={
            "resourceType": "dashboard",
            "dashboardUid": "test-uid",
            "timeRange": {"from": "now-6h", "to": "now"},
        },
        url_assert=[("from=now-6h", "from param"), ("to=now", "to param")],
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_query_params(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = (
        "Use the generate_deeplink tool to create a dashboard link for UID 'test-uid' "
        "with var-datasource=prometheus and refresh=30s as query parameters"
    )
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]
    tools_called: list = []

    response = await acompletion(model=model, messages=messages, tools=tools)

    # Same as previous: one round, require generate_deeplink with at least resourceType + dashboardUid
    if response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments) if tool_call.function.arguments else {}
            if tool_name == "generate_deeplink":
                assert args.get("resourceType") == "dashboard", f"Expected resourceType dashboard, got {args.get('resourceType')}"
                assert args.get("dashboardUid") == "test-uid", f"Expected dashboardUid test-uid, got {args.get('dashboardUid')}"
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )
    else:
        raise AssertionError(
            "Expected LLM to call generate_deeplink. No tool calls."
        )

    final_response = await acompletion(model=model, messages=messages, tools=tools)
    final_content = final_response.choices[0].message.content or ""

    assert "/d/test-uid" in final_content, f"Expected dashboard URL with /d/test-uid, got: {final_content}"
    assert "var-datasource=prometheus" in final_content, f"Expected var-datasource=prometheus in URL, got: {final_content}"
    assert "refresh=30s" in final_content, f"Expected refresh=30s in URL, got: {final_content}"

    assert_expected_tools_called(tools_called, "generate_deeplink")
    test_case = LLMTestCase(input=prompt, actual_output=final_content, mcp_servers=[mcp_server], mcp_tools_called=tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    assert_test(test_case, [mcp_metric])
