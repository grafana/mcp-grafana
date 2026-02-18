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


def get_first_cloudwatch_datasource(datasources_data):
    """
    Returns the first datasource with type 'cloudwatch' from a list of datasources.
    Raises an AssertionError if none are found.
    """
    cw_datasources = [ds for ds in datasources_data if ds.get("type") == "cloudwatch"]
    assert len(cw_datasources) > 0, "No CloudWatch datasource found"
    return cw_datasources[0]


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_cloudwatch_list_namespaces(model: str, mcp_client: ClientSession):
    """Test that the LLM can list CloudWatch namespaces."""
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you list the available CloudWatch namespaces? Please use only the necessary tools to get this information."

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
    cw_ds = get_first_cloudwatch_datasource(datasources_data)
    print(f"\nFound CloudWatch datasource: {cw_ds['name']} (uid: {cw_ds['uid']})")

    # 2. List namespaces
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "list_cloudwatch_namespaces",
        required_params={"datasourceUid": cw_ds["uid"]}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    namespace_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain CloudWatch namespace names? It should mention namespaces like 'AWS/EC2', 'AWS/Lambda', 'Test/Application', or similar CloudWatch namespace patterns.",
        )
    )
    expect(input=prompt, output=content).to_pass(namespace_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_cloudwatch_list_metrics(model: str, mcp_client: ClientSession):
    """Test that the LLM can list CloudWatch metrics for a namespace."""
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you list the available metrics in the 'Test/Application' CloudWatch namespace? Please use only the necessary tools to get this information."

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
    cw_ds = get_first_cloudwatch_datasource(datasources_data)
    print(f"\nFound CloudWatch datasource: {cw_ds['name']} (uid: {cw_ds['uid']})")

    # 2. List metrics
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "list_cloudwatch_metrics",
        required_params={"datasourceUid": cw_ds["uid"], "namespace": "Test/Application"}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    metrics_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain CloudWatch metric names? It should mention metrics like 'CPUUtilization', 'MemoryUtilization', 'RequestCount', or similar metric names.",
        )
    )
    expect(input=prompt, output=content).to_pass(metrics_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_cloudwatch_query_metrics(model: str, mcp_client: ClientSession):
    """Test that the LLM can query CloudWatch metrics."""
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you query the CPUUtilization metric from the 'Test/Application' namespace in CloudWatch for the last hour? Use the 'test-service' ServiceName dimension. Please use only the necessary tools."

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
    cw_ds = get_first_cloudwatch_datasource(datasources_data)
    print(f"\nFound CloudWatch datasource: {cw_ds['name']} (uid: {cw_ds['uid']})")

    # 2. Query metrics
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "query_cloudwatch",
        required_params={"datasourceUid": cw_ds["uid"]}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    query_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response provide information about CloudWatch metric data? It should either show metric values/datapoints, mention that data was retrieved, or explain that no data was found in the specified time range. Generic error messages don't count.",
        )
    )
    expect(input=prompt, output=content).to_pass(query_checker)
