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
from dotenv import load_dotenv

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
async def test_dashboard_panel_queries_tool(model: str, mcp_client: ClientSession):
    tools = await mcp_client.list_tools()
    prompt = "Can you list the panel queries for the dashboard with UID fe9gm6guyzi0wd?"

    messages: list[Message] = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]
    tools = [convert_tool(t) for t in tools.tools]

    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Call the LLM including the tool call result.
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Check that there's a dashboard panel queries tool call.
    assert isinstance(response, ModelResponse)
    messages.extend(
        await assert_and_handle_tool_call(
            response,
            mcp_client,
            "get_dashboard_panel_queries",
            {"uid": "fe9gm6guyzi0wd"}
        )
    )

    # Call the LLM including the tool call result.
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    # Check that the response has a list of panel queries along with their titles.
    content = response.choices[0].message.content
    panel_queries_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain specific information about the panel queries and titles for a grafana dashboard?",
        )
    )
    print("content", content)
    expect(input=prompt, output=content).to_pass(panel_queries_checker)

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

        if expected_args:
            for key, value in expected_args.items():
                assert key in arguments, (
                    f"Missing required argument '{key}' in tool call"
                )
                assert arguments[key] == value, (
                    f"Argument '{key}' has wrong value. Expected: {value}, Got: {arguments[key]}"
                )

        print(f"calling tool: {tool_call.function.name}({arguments})")
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
