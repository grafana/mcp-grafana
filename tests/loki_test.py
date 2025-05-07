import json
import os

from litellm.types.utils import ModelResponse
import pytest
from langevals import expect
from langevals_langevals.llm_boolean import (
    CustomLLMBooleanEvaluator,
    CustomLLMBooleanSettings,
)
from litellm import Message, acompletion
from mcp import ClientSession
from mcp.client.sse import sse_client
from dotenv import load_dotenv

from utils import (
    get_converted_tools,
    llm_tool_call_sequence,
)

load_dotenv()

DEFAULT_GRAFANA_URL = "http://localhost:3000"
DEFAULT_MCP_URL = "http://localhost:8000/sse"

models = ["gpt-4o", "claude-3-5-sonnet-20240620"]

pytestmark = pytest.mark.anyio


@pytest.fixture
def mcp_url():
    return os.environ.get("MCP_GRAFANA_URL", DEFAULT_MCP_URL)


@pytest.fixture
def grafana_headers():
    headers = {
        "X-Grafana-URL": os.environ.get("GRAFANA_URL", DEFAULT_GRAFANA_URL),
    }
    if key := os.environ.get("GRAFANA_API_KEY"):
        headers["X-Grafana-API-Key"] = key
    return headers


@pytest.fixture
async def mcp_client(mcp_url, grafana_headers):
    async with sse_client(mcp_url, headers=grafana_headers) as (
        read,
        write,
    ):
        async with ClientSession(read, write) as session:
            await session.initialize()
            yield session


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_loki_logs_tool(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you list the last 10 log lines from all containers using any available Loki datasource? Give me the raw log lines. Please use only the necessary tools to get this information."

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # 1. List datasources
    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "list_datasources"
    )
    datasources_response = messages[-1].content
    datasources_data = json.loads(datasources_response)
    loki_ds = get_first_loki_datasource(datasources_data)
    print(f"\nFound Loki datasource: {loki_ds['name']} (uid: {loki_ds['uid']})")

    # 2. Query logs
    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "query_loki_logs", {"datasourceUid": loki_ds["uid"]}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    log_lines_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain specific information that could only come from a Loki datasource? This could be actual log lines with timestamps, container names, or a summary that references specific log data. The response should show evidence of real data rather than generic statements.",
        )
    )
    expect(input=prompt, output=content).to_pass(log_lines_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_loki_container_labels(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Can you list the values for the label container in any available loki datasource? Please use only the necessary tools to get this information."

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # 1. List datasources
    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "list_datasources"
    )
    datasources_response = messages[-1].content
    datasources_data = json.loads(datasources_response)
    loki_ds = get_first_loki_datasource(datasources_data)
    print(f"\nFound Loki datasource: {loki_ds['name']} (uid: {loki_ds['uid']})")

    # 2. List label values for 'container'
    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "list_loki_label_values",
        {"datasourceUid": loki_ds["uid"], "labelName": "container"}
    )

    # 3. Final LLM response
    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    label_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response provide a clear and organized list of container names found in the logs? It should present the container names in a readable format and may include additional context about their usage.",
        )
    )
    expect(input=prompt, output=content).to_pass(label_checker)

def get_first_loki_datasource(datasources_data):
    """
    Returns the first datasource with type 'loki' from a list of datasources.
    Raises an AssertionError if none are found.
    """
    loki_datasources = [ds for ds in datasources_data if ds.get("type") == "loki"]
    assert len(loki_datasources) > 0, "No Loki datasource found"
    return loki_datasources[0]
