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
        "Use the list_clickhouse_tables tool to list all tables in the 'test' database "
        "of the ClickHouse datasource with UID 'clickhouse'."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    ch_calls = [tc for tc in tools_called if tc.name == "list_clickhouse_tables"]
    assert ch_calls, (
        f"list_clickhouse_tables was not in tools_called. "
        f"Actually called: {[tc.name for tc in tools_called]}"
    )
    assert ch_calls[0].args.get("datasourceUid") == "clickhouse", (
        f"Expected datasourceUid='clickhouse', got {ch_calls[0].args.get('datasourceUid')!r}"
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
        "Use the describe_clickhouse_table tool to describe the schema of the 'logs' table "
        "in the 'test' database of the ClickHouse datasource with UID 'clickhouse'. "
        "Show me the column names and types."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    ch_calls = [tc for tc in tools_called if tc.name == "describe_clickhouse_table"]
    assert ch_calls, (
        f"describe_clickhouse_table was not in tools_called. "
        f"Actually called: {[tc.name for tc in tools_called]}"
    )
    args = ch_calls[0].args
    assert args.get("datasourceUid") == "clickhouse", (
        f"Expected datasourceUid='clickhouse', got {args.get('datasourceUid')!r}"
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
        "Use the query_clickhouse tool to query the last 10 log entries from the 'logs' table "
        "in the 'test' database of the ClickHouse datasource with UID 'clickhouse'. "
        "Show me the service names and severity levels."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    ch_calls = [tc for tc in tools_called if tc.name == "query_clickhouse"]
    assert ch_calls, (
        f"query_clickhouse was not in tools_called. "
        f"Actually called: {[tc.name for tc in tools_called]}"
    )
    assert ch_calls[0].args.get("datasourceUid") == "clickhouse", (
        f"Expected datasourceUid='clickhouse', got {ch_calls[0].args.get('datasourceUid')!r}"
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
