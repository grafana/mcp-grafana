import pytest
from mcp import ClientSession
from mcp.types import ImageContent

from conftest import models
from utils import (
    MCP_EVAL_THRESHOLD,
    assert_expected_tools_called,
    run_llm_tool_loop,
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
    dashboard_uid = "fe9gm6guyzi0wd"
    prompt = (
        f"Use get_panel_image with dashboardUid '{dashboard_uid}' to render an image of that dashboard. "
        "Return a brief confirmation that the image was rendered."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_expected_tools_called(tools_called, "get_panel_image")
    panel_calls = [tc for tc in tools_called if tc.name == "get_panel_image"]
    assert panel_calls, "get_panel_image was not in tools_called"
    args = panel_calls[0].args
    assert args.get("dashboardUid") == dashboard_uid, (
        f"Expected dashboardUid={dashboard_uid!r}, got {args.get('dashboardUid')!r}"
    )
    mcp_tc = panel_calls[0]
    if mcp_tc.result.content:
        content_item = mcp_tc.result.content[0]
        assert isinstance(content_item, ImageContent)
        assert content_item.type == "image"
        assert content_item.mimeType == "image/png"
        assert len(content_item.data) > 0
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
