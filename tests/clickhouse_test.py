import json

import pytest
from langevals import expect
from langevals_langevals.llm_boolean import (
    CustomLLMBooleanEvaluator,
    CustomLLMBooleanSettings,
)
from litellm import Message, acompletion
from mcp import ClientSession

from conftest import models
from utils import (
    get_converted_tools,
    flexible_tool_call,
)

pytestmark = pytest.mark.anyio


def get_first_clickhouse_datasource(datasources_data):
    """
    Returns the first datasource with type 'grafana-clickhouse-datasource' from a list of datasources.
    Raises an AssertionError if none are found.
    """
    clickhouse_datasources = [
        ds for ds in datasources_data
        if ds.get("type") == "grafana-clickhouse-datasource"
    ]
    assert len(clickhouse_datasources) > 0, "No ClickHouse datasource found"
    return clickhouse_datasources[0]


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_clickhouse_list_tables(model: str, mcp_client: ClientSession):
    """Test that the LLM can list tables in a ClickHouse database."""
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you list all tables in the 'test' database of the ClickHouse datasource? Please use only the necessary tools to get this information."

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # 1. List datasources
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "list_datasources"
    )
    datasources_response = messages[-1].content
    datasources_data = json.loads(datasources_response)
    ch_ds = get_first_clickhouse_datasource(datasources_data)
    print(f"\nFound ClickHouse datasource: {ch_ds['name']} (uid: {ch_ds['uid']})")

    # 2. List tables
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "list_clickhouse_tables",
        required_params={"datasourceUid": ch_ds["uid"]}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    table_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain actual table names from a ClickHouse database? It should mention specific tables like 'logs' or 'metrics' or similar database table names. The response should show evidence of real data rather than generic statements.",
        )
    )
    expect(input=prompt, output=content).to_pass(table_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_clickhouse_describe_table(model: str, mcp_client: ClientSession):
    """Test that the LLM can describe a ClickHouse table schema."""
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you describe the schema of the 'logs' table in the 'test' database of the ClickHouse datasource? Show me the column names and types. Please use only the necessary tools to get this information."

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # 1. List datasources
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "list_datasources"
    )
    datasources_response = messages[-1].content
    datasources_data = json.loads(datasources_response)
    ch_ds = get_first_clickhouse_datasource(datasources_data)
    print(f"\nFound ClickHouse datasource: {ch_ds['name']} (uid: {ch_ds['uid']})")

    # 2. Describe table
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "describe_clickhouse_table",
        required_params={"datasourceUid": ch_ds["uid"], "table": "logs", "database": "test"}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    schema_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain actual column information from a ClickHouse table schema? It should mention specific column names like 'Timestamp', 'Body', 'ServiceName', 'SeverityText' and their types like 'DateTime64', 'String'. The response should show evidence of real schema data.",
        )
    )
    expect(input=prompt, output=content).to_pass(schema_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_clickhouse_query_logs(model: str, mcp_client: ClientSession):
    """Test that the LLM can query logs from a ClickHouse database."""
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you query the last few log entries from the 'logs' table in the 'test' database of the ClickHouse datasource? Show me the service names and severity levels. Please use only the necessary tools to get this information."

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # 1. List datasources
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "list_datasources"
    )
    datasources_response = messages[-1].content
    datasources_data = json.loads(datasources_response)
    ch_ds = get_first_clickhouse_datasource(datasources_data)
    print(f"\nFound ClickHouse datasource: {ch_ds['name']} (uid: {ch_ds['uid']})")

    # 2. Query logs
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "query_clickhouse",
        required_params={"datasourceUid": ch_ds["uid"]}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    query_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain actual log data from a ClickHouse query? It should show specific service names like 'test-service' or 'api-gateway', and severity levels like 'INFO', 'ERROR', 'DEBUG', 'WARN'. The response should show evidence of real query results rather than generic statements.",
        )
    )
    expect(input=prompt, output=content).to_pass(query_checker)
