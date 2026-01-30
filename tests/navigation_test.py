import pytest
from mcp import ClientSession

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop


pytestmark = pytest.mark.anyio


async def _run_deeplink_test_with_expected_args(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
    prompt: str,
    criteria: str,
    expected_tool_args: dict,
    url_assert: tuple[str, str] | list[tuple[str, str]] | None = None,
):
    """
    Run LLM tool loop, then validate that generate_deeplink was called with expected_tool_args.
    """
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    deeplink_calls = [tc for tc in tools_called if tc.name == "generate_deeplink"]
    if not deeplink_calls:
        raise AssertionError(
            f"Expected LLM to call generate_deeplink with args {expected_tool_args}. "
            f"Actually called: {[tc.name for tc in tools_called]}. Content: {final_content[:200]}..."
        )
    args = deeplink_calls[0].args
    for key, expected_value in expected_tool_args.items():
        assert key in args, f"Expected parameter '{key}' in tool arguments, got: {args}"
        if expected_value is not None:
            actual = args[key]
            if isinstance(expected_value, dict) and isinstance(actual, dict):
                for k, v in expected_value.items():
                    assert k in actual and actual[k] == v, (
                        f"Expected {key}.{k}={v!r}, got {actual.get(k)!r}"
                    )
            else:
                assert actual == expected_value, (
                    f"Expected {key}={expected_value!r}, got {key}={actual!r}"
                )

    if url_assert:
        pairs = [url_assert] if isinstance(url_assert, tuple) else url_assert
        for substring, desc in pairs:
            assert substring in final_content, f"Expected {desc}, got: {final_content}"

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        criteria,
        expected_tools="generate_deeplink",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_dashboard_deeplink(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Please create a dashboard deeplink for dashboard with UID 'test-uid'."
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with /d/ path and the dashboard UID?",
        expected_tool_args={"resourceType": "dashboard", "dashboardUid": "test-uid"},
        url_assert=("/d/test-uid", "dashboard URL with /d/test-uid"),
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_panel_deeplink(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Generate a deeplink for panel 5 in dashboard with UID 'test-uid'"
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with viewPanel parameter?",
        expected_tool_args={
            "resourceType": "panel",
            "dashboardUid": "test-uid",
            "panelId": 5,
        },
        url_assert=("viewPanel=5", "panel URL with viewPanel=5"),
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_explore_deeplink(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Generate a deeplink for Grafana Explore with datasource 'test-uid'"
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with /explore path?",
        expected_tool_args={"resourceType": "explore", "datasourceUid": "test-uid"},
        url_assert=("/explore", "explore URL with /explore path"),
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_time_range(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = "Generate a dashboard deeplink for 'test-uid' showing the last 6 hours"
    await _run_deeplink_test_with_expected_args(
        model,
        mcp_client,
        mcp_transport,
        prompt,
        "Does the response contain a URL with time range parameters?",
        expected_tool_args={
            "resourceType": "dashboard",
            "dashboardUid": "test-uid",
            "timeRange": {"from": "now-6h", "to": "now"},
        },
        url_assert=[("from=now-6h", "from param"), ("to=now", "to param")],
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_query_params(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    prompt = (
        "Use the generate_deeplink tool to create a dashboard link for UID 'test-uid' "
        "with var-datasource=prometheus and refresh=30s as query parameters"
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_expected_tools_called(tools_called, "generate_deeplink")
    deeplink_calls = [tc for tc in tools_called if tc.name == "generate_deeplink"]
    assert deeplink_calls, "Expected LLM to call generate_deeplink"
    args = deeplink_calls[0].args
    assert args.get("resourceType") == "dashboard", f"Expected resourceType dashboard, got {args.get('resourceType')}"
    assert args.get("dashboardUid") == "test-uid", f"Expected dashboardUid test-uid, got {args.get('dashboardUid')}"

    assert "/d/test-uid" in final_content, f"Expected dashboard URL with /d/test-uid, got: {final_content}"
    assert "var-datasource=prometheus" in final_content, f"Expected var-datasource=prometheus in URL, got: {final_content}"
    assert "refresh=30s" in final_content, f"Expected refresh=30s in URL, got: {final_content}"

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        expected_tools="generate_deeplink",
    )
