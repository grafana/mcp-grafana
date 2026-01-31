import pytest
from mcp import ClientSession
from mcp.types import ImageContent

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop


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

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response confirm that a dashboard image was rendered or provided "
        "(e.g. by stating the image was rendered, or that get_panel_image was used successfully)? "
        "A brief confirmation is sufficient; the response need not include the image data.",
        expected_tools="get_panel_image",
    )
