import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop

pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_cloudwatch_list_namespaces(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list CloudWatch namespaces."""
    prompt = "List all CloudWatch namespaces available on the CloudWatch datasource in Grafana. Use the us-east-1 region."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain CloudWatch namespace names? "
        "It should mention specific namespaces like 'AWS/EC2', 'AWS/Lambda', 'Test/Application', "
        "or similar CloudWatch namespace patterns. ",
        expected_tools="list_cloudwatch_namespaces",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_cloudwatch_list_metrics(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list CloudWatch metrics for a namespace."""
    prompt = "List the CloudWatch metrics available in the 'Test/Application' namespace. Use the us-east-1 region."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain CloudWatch metric names from the Test/Application namespace? "
        "It should mention specific metrics like 'CPUUtilization', 'MemoryUtilization', 'RequestCount', "
        "or similar metric names. ",
        expected_tools="list_cloudwatch_metrics",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_cloudwatch_query_metrics(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can query CloudWatch metrics."""
    prompt = (
        "Query the CloudWatch CPUUtilization metric from the 'Test/Application' namespace "
        "for the 'test-service' ServiceName dimension over the last hour."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response provide information about CloudWatch metric data? "
        "It should either show metric values or datapoints, mention that data was retrieved, "
        "or explain that no data was found in the specified time range. "
        "Generic error messages don't count.",
        expected_tools="query_cloudwatch",
    )
