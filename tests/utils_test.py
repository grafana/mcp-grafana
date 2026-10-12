from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from mcp.types import CallToolResult, ImageContent, TextContent
from deepeval.test_case import LLMTestCaseParams, MCPServer, MCPToolCall

import utils


@pytest.mark.anyio
async def test_output_judge_receives_tool_results(monkeypatch):
    captured = {}

    def capture(test_case, metrics):
        captured["case"] = test_case
        captured["metrics"] = metrics

    monkeypatch.setattr(utils, "assert_test", capture)
    monkeypatch.setattr(utils, "MCPUseMetric", lambda **kwargs: SimpleNamespace(**kwargs))
    monkeypatch.setattr(utils, "GEval", lambda **kwargs: SimpleNamespace(**kwargs))
    tool_call = MCPToolCall(
        name="query_elasticsearch",
        args={"index": "test-logs-2024"},
        result=CallToolResult(content=[TextContent(type="text", text='{"message":"real log"}')]),
    )
    server = MCPServer(server_name="mcp-grafana-stdio", transport="stdio", available_tools=[])

    utils.assert_mcp_eval(
        "Show the logs", "The log says real log", [tool_call], server,
        output_criteria="Check the response against the returned logs.",
    )

    assert captured["case"].retrieval_context == [
        'query_elasticsearch result: {"message":"real log"}'
    ]
    assert LLMTestCaseParams.RETRIEVAL_CONTEXT in captured["metrics"][1].evaluation_params


@pytest.mark.anyio
async def test_tool_result_text_matches_the_first_content_seen_by_the_agent():
    image = ImageContent(type="image", data="aGVsbG8=", mimeType="image/png")
    text = TextContent(type="text", text="Image permalink")
    assert utils.tool_result_text(CallToolResult(content=[image, text])) == "[Image content]"
    assert utils.tool_result_text(CallToolResult(content=[text, image])) == "Image permalink"
    assert utils.tool_result_text(CallToolResult(content=[])) == ""


@pytest.mark.anyio
async def test_tool_loop_instructs_model_to_use_mcp_evidence(monkeypatch):
    server = MCPServer(server_name="mcp-grafana-stdio", transport="stdio", available_tools=[])
    monkeypatch.setattr(utils, "make_mcp_server", AsyncMock(return_value=server))
    monkeypatch.setattr(utils, "get_converted_tools", AsyncMock(return_value=[]))
    response = SimpleNamespace(choices=[SimpleNamespace(message=SimpleNamespace(content="No data", tool_calls=[]))])
    completion = AsyncMock(return_value=response)
    monkeypatch.setattr(utils, "acompletion", completion)

    await utils.run_llm_tool_loop("gpt-4o", object(), "stdio", "Show the Grafana logs")

    system_message = completion.call_args.kwargs["messages"][0]
    assert "Use the available MCP tools" in system_message.content
    assert "Never invent results" in system_message.content
