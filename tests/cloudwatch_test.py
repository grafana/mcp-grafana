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
    prompt = (
        "I have a CloudWatch datasource configured in Grafana. "
        "Can you list the actual CloudWatch namespaces it has access to?"
    )
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
        "or similar CloudWatch namespace patterns. "
        "The response should show evidence of real data rather than generic statements.",
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
    prompt = (
        "I'd like to see what metrics exist in the 'Test/Application' namespace "
        "on my CloudWatch datasource. Can you list them?"
    )
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
        "or similar metric names. "
        "The response should show evidence of real data rather than generic statements.",
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
        "I need to check the CPUUtilization for the 'test-service' in CloudWatch. "
        "Query the 'Test/Application' namespace using the ServiceName dimension "
        "and show me the data from the last hour."
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
