import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop


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
    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain specific information that could only come from a Loki datasource? "
        "This could be actual log lines with timestamps, container names, or a summary that references "
        "specific log data. The response should show evidence of real data rather than generic statements.",
        expected_tools="query_loki_logs",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_loki_container_labels(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = (
        "List the values for the label 'container' for the last 10 minutes from the Loki datasource."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    # LLMs often discover Loki via search_dashboards/get_dashboard_panel_queries first;
    # MCPUseMetric penalizes that (score ~0.5). Use threshold 0.5 so exploratory tool use still passes.
    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response provide a list of container names found in the logs? "
        "It should present the container names in a readable format and may include additional "
        "context about their usage.",
        expected_tools=None,
        mcp_threshold=0.5,
    )
