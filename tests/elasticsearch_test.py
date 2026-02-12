import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop


pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_elasticsearch_query_logs(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = (
        "Can you query the Elasticsearch datasource for the last 10 log entries "
        "from the 'test-logs-2024' index? Show me the log messages and their severity levels."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain specific log data that could only come from an Elasticsearch datasource? "
        "This could include log messages with levels like 'info', 'error', 'warn', or 'debug', "
        "service names like 'api-gateway' or 'auth-service', or HTTP status codes. "
        "The response should show evidence of real data rather than generic statements.",
        expected_tools="query_elasticsearch",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_elasticsearch_query_errors(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = (
        "Search for error-level logs in the Elasticsearch datasource using the 'test-logs-2024' index. "
        "Use the query 'level:error' to find them. What errors occurred?"
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain information about error-level log entries from Elasticsearch? "
        "It should reference specific error messages such as database timeouts or JSON parsing failures. "
        "The response should show evidence of real error data rather than generic statements.",
        expected_tools="query_elasticsearch",
    )
