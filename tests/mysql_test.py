import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop

pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_mysql_list_tables(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list tables in a MySQL database."""
    prompt = (
        "Can you list all tables in the MySQL datasource? "
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
        "Does the response contain actual table names from a MySQL database? "
        "It should mention specific tables like 'logs' or 'metrics'. "
        "The response should show evidence of real data rather than generic statements.",
        expected_tools="list_sql_tables",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_mysql_describe_table(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can describe a MySQL table schema."""
    prompt = (
        "Can you describe the schema of the 'logs' table in the 'test' database "
        "of the MySQL datasource? Show me the column names and types."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain actual column information from a MySQL table schema? "
        "It should mention specific column names like 'id', 'timestamp', 'body', 'service_name', 'severity_text' "
        "and their types. The response should show evidence of real schema data.",
        expected_tools="describe_sql_table",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_mysql_query_logs(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can query logs from a MySQL database."""
    prompt = (
        "Can you query the last few log entries from the 'logs' table in the 'test' database "
        "of the MySQL datasource? Show me the service names and severity levels."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain actual log data from a MySQL query? "
        "It should show specific service names like 'test-service' or 'api-gateway', "
        "and severity levels like 'INFO', 'ERROR', 'DEBUG', 'WARN'. "
        "The response should show evidence of real query results rather than generic statements.",
        expected_tools="query_sql",
    )
