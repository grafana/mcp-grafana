import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop

pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_cloudwatch_list_log_groups(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list CloudWatch log groups."""
    prompt = "List all CloudWatch log groups available on the CloudWatch datasource in Grafana. Use the us-east-1 region."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain CloudWatch log group names? "
        "It should mention specific log groups like 'test-application-logs' "
        "or similar log group patterns. ",
        expected_tools="list_cloudwatch_log_groups",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_cloudwatch_query_logs(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can query CloudWatch Logs Insights."""
    prompt = (
        "Query CloudWatch Logs Insights for ERROR messages in the 'test-application-logs' log group "
        "over the last hour. Use the us-east-1 region."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response provide information about CloudWatch log data? "
        "It should either show log entries or messages, mention that logs were retrieved, "
        "or explain that no log data was found in the specified time range. "
        "Generic error messages don't count.",
        expected_tools="query_cloudwatch_logs",
    )
