import json
import pytest
from litellm import Message, acompletion
from mcp import ClientSession
from mcp.types import ImageContent

from conftest import models
from utils import (
    get_converted_tools,
    MCP_EVAL_THRESHOLD,
    assert_expected_tools_called,
    make_mcp_server,
    call_tool_and_record,
)
from deepeval import assert_test
from deepeval.metrics import MCPUseMetric, GEval
from deepeval.test_case import LLMTestCase, LLMTestCaseParams


pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_get_panel_image(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    mcp_server = await make_mcp_server(mcp_client, transport=mcp_transport)
    tools = await get_converted_tools(mcp_client)
    dashboard_uid = "fe9gm6guyzi0wd"
    prompt = f"Render an image of the dashboard with UID {dashboard_uid}"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]
    tools_called: list = []

    response = await acompletion(
        model=model,
        messages=messages,
        tools=tools,
    )

    if response.choices and response.choices[0].message.tool_calls:
        for tool_call in response.choices[0].message.tool_calls:
            tool_name = tool_call.function.name
            args = json.loads(tool_call.function.arguments) if tool_call.function.arguments else {}
            if tool_name == "get_panel_image":
                assert args.get("dashboardUid") == dashboard_uid, (
                    f"Expected dashboardUid={dashboard_uid!r}, got {args.get('dashboardUid')!r}"
                )
            result_text, mcp_tc = await call_tool_and_record(mcp_client, tool_name, args)
            tools_called.append(mcp_tc)
            # Verify we got image content
            if mcp_tc.result.content:
                content_item = mcp_tc.result.content[0]
                assert isinstance(content_item, ImageContent)
                assert content_item.type == "image"
                assert content_item.mimeType == "image/png"
                assert len(content_item.data) > 0
            messages.append(response.choices[0].message)
            messages.append(
                Message(role="tool", tool_call_id=tool_call.id, content=result_text)
            )

    final_response = await acompletion(model=model, messages=messages, tools=tools)
    final_content = final_response.choices[0].message.content or ""

    assert_expected_tools_called(tools_called, "get_panel_image")
    test_case = LLMTestCase(input=prompt, actual_output=final_content, mcp_servers=[mcp_server], mcp_tools_called=tools_called)

    mcp_metric = MCPUseMetric(threshold=MCP_EVAL_THRESHOLD)
    output_metric = GEval(
        name="OutputQuality",
        criteria=(
            "Does the response confirm that a dashboard image was rendered or provided "
            "(e.g. by stating the image was rendered, or that get_panel_image was used successfully)? "
            "A brief confirmation is sufficient; the response need not include the image data."
        ),
        evaluation_params=[LLMTestCaseParams.INPUT, LLMTestCaseParams.ACTUAL_OUTPUT],
        threshold=MCP_EVAL_THRESHOLD,
    )

    assert_test(test_case, [mcp_metric, output_metric])
