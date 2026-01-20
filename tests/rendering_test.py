import json
import pytest
from mcp import ClientSession

from conftest import models
from utils import (
    get_converted_tools,
    llm_tool_call_sequence,
)
from litellm import Message, acompletion
from mcp.types import ImageContent

pytestmark = pytest.mark.anyio

@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_get_panel_image(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    # Use the demo dashboard UID
    dashboard_uid = "fe9gm6guyzi0wd"
    
    prompt = f"Render an image of the dashboard with UID {dashboard_uid}"
    
    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # We manually handle the tool call sequence because built-in helper expects TextContent
    
    # 1. Ask LLM
    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )
    
    # 2. Check tool call
    tool_calls = response.choices[0].message.tool_calls
    assert len(tool_calls) == 1
    tool_call = tool_calls[0]
    assert tool_call.function.name == "get_panel_image"
    
    args = json.loads(tool_call.function.arguments)
    assert args["dashboardUid"] == dashboard_uid
    
    # 3. Call tool
    result = await mcp_client.call_tool(tool_call.function.name, args)
    
    # 4. Verify Image Content
    assert len(result.content) == 1
    content = result.content[0]
    assert isinstance(content, ImageContent)
    assert content.type == "image"
    assert content.mimeType == "image/png"
    assert len(content.data) > 0
    
    print("Successfully rendered dashboard image")
