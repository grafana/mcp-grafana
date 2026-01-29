import pytest
from mcp import ClientSession

from conftest import models
from utils import (
    MCP_EVAL_THRESHOLD,
    assert_expected_tools_called,
    run_llm_tool_loop,
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
    prompt = (
        "Can you query the last 10 log lines from container 'mcp-grafana-grafana-1'? Give me the raw log lines."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
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
        threshold=0.5,
    )

    assert_test(test_case, [mcp_metric, output_metric])


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_loki_container_labels(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = (
        "List the values for the label 'container' for the last 10 minutes using Loki. "
        "If you don't have a Loki datasource UID, use list_datasources with type 'loki' first to get one, "
        "then use list_loki_label_values with that datasourceUid and labelName 'container'. Return the list of container names."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
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
