import json
import pytest
from litellm import Message, acompletion
from mcp import ClientSession
from mcp.types import TextContent

from conftest import models
from utils import (
    assert_llm_output_passes,
    get_converted_tools,
    llm_tool_call_sequence,
    flexible_tool_call,
)

pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_dashboard_deeplink(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)

    prompt = """Please create a dashboard deeplink for dashboard with UID 'test-uid'."""

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {"resourceType": "dashboard", "dashboardUid": "test-uid"}
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    assert "/d/test-uid" in content, f"Expected dashboard URL with /d/test-uid, got: {content}"
    print("Dashboard deeplink content:", content)
    assert_llm_output_passes(
        prompt,
        content,
        "Does the response contain a URL with /d/ path and the dashboard UID?",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_panel_deeplink(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a deeplink for panel 5 in dashboard with UID 'test-uid'"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {
            "resourceType": "panel",
            "dashboardUid": "test-uid",
            "panelId": 5
        }
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    assert "viewPanel=5" in content, f"Expected panel URL with viewPanel=5, got: {content}"
    print("Panel deeplink content:", content)
    assert_llm_output_passes(
        prompt,
        content,
        "Does the response contain a URL with viewPanel parameter?",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_explore_deeplink(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a deeplink for Grafana Explore with datasource 'test-uid'"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {"resourceType": "explore", "datasourceUid": "test-uid"}
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    assert "/explore" in content, f"Expected explore URL with /explore path, got: {content}"
    print("Explore deeplink content:", content)
    assert_llm_output_passes(
        prompt,
        content,
        "Does the response contain a URL with /explore path?",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_time_range(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a dashboard deeplink for 'test-uid' showing the last 6 hours"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {
            "resourceType": "dashboard",
            "dashboardUid": "test-uid",
            "timeRange": {
                "from": "now-6h",
                "to": "now"
            }
        }
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    assert "from=now-6h" in content and "to=now" in content, f"Expected time range parameters, got: {content}"
    print("Time range deeplink content:", content)
    assert_llm_output_passes(
        prompt,
        content,
        "Does the response contain a URL with time range parameters?",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_query_params(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Use the generate_deeplink tool to create a dashboard link for UID 'test-uid' with var-datasource=prometheus and refresh=30s as query parameters"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    # Use flexible tool call with required parameters
    messages = await flexible_tool_call(
        model, messages, tools, mcp_client, "generate_deeplink",
        required_params={"resourceType": "dashboard", "dashboardUid": "test-uid"}
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    # Verify both specific query parameters are in the final URL
    assert "var-datasource=prometheus" in content, f"Expected var-datasource=prometheus in URL, got: {content}"
    assert "refresh=30s" in content, f"Expected refresh=30s in URL, got: {content}"
    print("Custom params deeplink content:", content)
    assert_llm_output_passes(
        prompt,
        content,
        "Does the response contain a URL with custom query parameters?",
    )


