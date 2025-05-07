import json
from litellm.types.utils import ModelResponse
from litellm import acompletion, Choices, Message
from mcp.types import TextContent, Tool

async def assert_and_handle_tool_call(
    response: ModelResponse,
    mcp_client,
    expected_tool: str,
    expected_args: dict = None,
) -> list:
    messages = []
    tool_calls = []
    for c in response.choices:
        assert isinstance(c, Choices)
        tool_calls.extend(c.message.tool_calls or [])
        messages.append(c.message)
    assert len(tool_calls) == 1
    for tool_call in tool_calls:
        assert tool_call.function.name == expected_tool
        arguments = (
            {}
            if len(tool_call.function.arguments) == 0
            else json.loads(tool_call.function.arguments)
        )
        if expected_args:
            for key, value in expected_args.items():
                assert key in arguments
                assert arguments[key] == value
        result = await mcp_client.call_tool(tool_call.function.name, arguments)
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

async def llm_tool_call_sequence(
    model, messages, tools, mcp_client, tool_name, tool_args=None
):
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )
    assert isinstance(response, ModelResponse)
    messages.extend(
        await assert_and_handle_tool_call(
            response, mcp_client, tool_name, tool_args or {}
        )
    )
    return messages

async def get_converted_tools(mcp_client):
    tools = await mcp_client.list_tools()
    return [convert_tool(t) for t in tools.tools]