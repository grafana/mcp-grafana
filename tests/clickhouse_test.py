import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop

pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_clickhouse_list_tables(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list tables in a ClickHouse database."""
    prompt = (
        "Can you list all tables in the ClickHouse datasource? "
        "I'd like to see what tables are available in the 'test' database."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain actual table names from a ClickHouse database? "
        "It should mention specific tables like 'logs' or 'metrics' or similar database table names. "
        "The response should show evidence of real data rather than generic statements.",
        expected_tools="list_clickhouse_tables",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_clickhouse_describe_table(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can describe a ClickHouse table schema."""
    prompt = (
        "Can you describe the schema of the 'logs' table in the 'test' database "
        "of the ClickHouse datasource? Show me the column names and types."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain actual column information from a ClickHouse table schema? "
        "It should mention specific column names like 'Timestamp', 'Body', 'ServiceName', 'SeverityText' "
        "and their types like 'DateTime64', 'String'. The response should show evidence of real schema data.",
        expected_tools="describe_clickhouse_table",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_clickhouse_query_logs(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can query logs from a ClickHouse database."""
    prompt = (
        "Can you query the last few log entries from the 'logs' table in the 'test' database "
        "of the ClickHouse datasource? Show me the service names and severity levels."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain actual log data from a ClickHouse query? "
        "It should show specific service names like 'test-service' or 'api-gateway', "
        "and severity levels like 'INFO', 'ERROR', 'DEBUG', 'WARN'. "
        "The response should show evidence of real query results rather than generic statements.",
        expected_tools="query_clickhouse",
    )
