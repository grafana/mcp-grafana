import os
from typing import List, Union

from litellm import Message, acompletion
from mcp import ClientSession
from mcp.types import TextContent, ImageContent, CallToolResult, Tool
from deepeval.test_case import MCPServer, MCPToolCall

# Default threshold for MCPUseMetric and GEval (0–1). Used by all MCP eval tests.
MCP_EVAL_THRESHOLD = 0.5


def convert_tool(tool: Tool) -> dict:
    """Convert an MCP Tool to OpenAI-style function schema for the LLM."""
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


async def get_converted_tools(client: ClientSession) -> list:
    """List MCP tools and return them as OpenAI-style function list for the LLM."""
    tool_list = await client.list_tools()
    return [convert_tool(t) for t in tool_list.tools]


async def make_mcp_server(client: ClientSession, transport: str = "sse") -> MCPServer:
    """Build DeepEval MCPServer from an MCP ClientSession (list_tools only)."""
    tool_list = await client.list_tools()
    if transport == "sse":
        mcp_url = os.environ.get("MCP_GRAFANA_URL", "http://localhost:8000")
        server_name = f"{mcp_url}/sse"
    elif transport == "streamable-http":
        mcp_url = os.environ.get("MCP_GRAFANA_URL", "http://localhost:8000")
        server_name = f"{mcp_url}/mcp"
    else:
        server_name = "mcp-grafana-stdio"
    return MCPServer(
        server_name=server_name,
        transport=transport,
        available_tools=tool_list.tools,
    )


async def call_tool_and_record(
    client: ClientSession, tool_name: str, args: dict
) -> tuple[str, MCPToolCall]:
    """
    Call an MCP tool and return (result text for message history, MCPToolCall for test case).
    """
    result: CallToolResult = await client.call_tool(tool_name, args)
    result_text = ""
    if result.content:
        for content_item in result.content:
            if isinstance(content_item, TextContent):
                result_text = content_item.text
                break
            if isinstance(content_item, ImageContent):
                result_text = "[Image content]"
                break
    tool_call = MCPToolCall(name=tool_name, args=args, result=result)
    return result_text, tool_call


async def run_llm_tool_loop(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
    prompt: str,
) -> tuple[str, List[MCPToolCall], MCPServer]:

    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]
    tools_called: List[MCPToolCall] = []

    response = await acompletion(model=model, messages=messages, tools=tools)

    while response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = (
                __import__("json").loads(tool_call.function.arguments)
                if tool_call.function.arguments
                else {}
            )
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )
        response = await acompletion(model=model, messages=messages, tools=tools)

    final_content = (
        (response.choices[0].message.content or "")
        if response.choices
        else ""
    )
    return final_content, tools_called, mcp_server


def assert_expected_tools_called(
    tools_called: List[MCPToolCall],
    expected: Union[str, List[str]],
) -> None:
    """
    Assert that each expected tool was called (order not enforced).
    Use this to document and enforce which tools a test expects the LLM to use.
    """
    expected_list = [expected] if isinstance(expected, str) else expected
    called_names = [tc.name for tc in tools_called]
    for name in expected_list:
        assert name in called_names, (
            f"Expected tool {name!r} to be called. Actually called: {called_names}"
        )
