import json
import os
from typing import Any

from litellm.types.utils import ModelResponse
import pytest
from langevals import expect
from langevals_langevals.llm_boolean import (
    CustomLLMBooleanEvaluator,
    CustomLLMBooleanSettings,
)
from litellm import ChatCompletionMessageToolCall, Choices, Message, acompletion
from mcp.types import TextContent, Tool
from mcp import ClientSession
from mcp.client.sse import sse_client

models = ["gpt-4o", "claude-3-5-sonnet-20240620"]

pytestmark = pytest.mark.anyio


@pytest.fixture(scope="session")
def anyio_backend():
    return "asyncio"


@pytest.fixture(scope="session")
def mcp_url():
    return os.environ.get("MCP_GRAFANA_URL", "http://localhost:8000/sse")


@pytest.fixture(scope="session")
def grafana_headers():
    headers = {
        "X-Grafana-URL": os.environ.get("GRAFANA_URL", "http://localhost:3000"),
    }
    if key := os.environ.get("GRAFANA_API_KEY"):
        headers["X-Grafana-API-Key"] = key
    return headers


@pytest.fixture(scope="session")
async def mcp_client(mcp_url, grafana_headers):
    async with sse_client(mcp_url, headers=grafana_headers) as (
        read,
        write,
    ):
        async with ClientSession(read, write) as session:
            # Initialize the connection
            await session.initialize()
            yield session


@pytest.mark.parametrize("model", models)
async def test_loki(model: str, mcp_client: ClientSession):
    tools = await mcp_client.list_tools()
    prompt = "what are the most recent log lines coming from Grafana?"

    messages: list[Message] = [
        Message(role="system", content="You are a helpful assistant."),  # type: ignore
        Message(role="user", content=prompt),  # type: ignore
    ]
    tools = [convert_tool(t) for t in tools.tools]

    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Check that there's a tool call.
    assert isinstance(response, ModelResponse)
    messages.extend(
        await assert_and_handle_tool_call(response, mcp_client, "list_datasources")
    )

    # Call the LLM including the tool call result.
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Check that there's a tool call.
    assert isinstance(response, ModelResponse)
    messages.extend(
        await assert_and_handle_tool_call(
            response,
            mcp_client,
            "list_loki_label_names",
            {"datasourceUid": "loki"},
        )
    )

    # Call the LLM including the tool call result.
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Check that there's a tool call.
    assert isinstance(response, ModelResponse)
    messages.extend(
        await assert_and_handle_tool_call(
            response,
            mcp_client,
            "list_loki_label_values",
            {"datasourceUid": "loki", "labelName": "container"},
        )
    )

    # Call the LLM including the tool call result.
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Check that there's another tool call.
    assert isinstance(response, ModelResponse)
    messages.extend(
        await assert_and_handle_tool_call(
            response,
            mcp_client,
            "query_loki_logs",
            {
                "datasourceUid": "loki",
                "logql": 'container="grafana"',
            },
        )
    )

    # Call the LLM including the tool call result.
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Check that the response has some log lines.
    content = response.choices[0].message.content  # type: ignore
    log_line_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response look like it contains Grafana log lines?",
        )
    )
    expect(input=prompt, output=content).to_pass(log_line_checker)


async def assert_and_handle_tool_call(
    response: ModelResponse,
    mcp_client: ClientSession,
    expected_tool: str,
    expected_args: dict[str, Any] | None = None,
) -> list[Message]:
    messages: list[Message] = []
    tool_calls: list[ChatCompletionMessageToolCall] = []
    for c in response.choices:
        assert isinstance(c, Choices)
        tool_calls.extend(c.message.tool_calls or [])
        # Add the message to the list of messages.
        # We'll need to send these back to the LLM with the tool call result.
        messages.append(c.message)

    # Check that the expected tool call is in the response.
    assert len(tool_calls) == 1

    # Call the tool(s) with the requested args.
    for tool_call in tool_calls:
        assert isinstance(tool_call.function.name, str)
        arguments = (
            {}
            if len(tool_call.function.arguments) == 0
            else json.loads(tool_call.function.arguments)
        )
        assert tool_call.function.name == expected_tool
        assert arguments == (expected_args or {})
        result = await mcp_client.call_tool(tool_call.function.name, arguments)
        # Assume each tool returns a single text content for now
        assert len(result.content) == 1
        assert isinstance(result.content[0], TextContent)
        messages.append(
            Message(
                role="tool", tool_call_id=tool_call.id, content=result.content[0].text
            )
        )
    return messages


def convert_tool(tool: Tool) -> dict:
    return {
        "type": "function",
        "function": {
            "name": tool.name,
            "description": tool.description,
            "parameters": {
                **tool.inputSchema,
                "properties": tool.inputSchema.get("properties", {}),
            },
        },
    }
