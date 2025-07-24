import pytest
from langevals import expect
from langevals_langevals.llm_boolean import (
    CustomLLMBooleanEvaluator,
    CustomLLMBooleanSettings,
)
from litellm import Message, acompletion
from mcp import ClientSession

from conftest import models
from utils import (
    get_converted_tools,
    llm_tool_call_sequence,
)

pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_dashboard_deeplink(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a deeplink for dashboard with UID 'test-dashboard'"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {"resourceType": "dashboard", "uid": "test-dashboard"}
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    dashboard_link_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain a valid Grafana dashboard deeplink URL with the format http://*/d/test-dashboard?",
        )
    )
    print("Dashboard deeplink content:", content)
    expect(input=prompt, output=content).to_pass(dashboard_link_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_panel_deeplink(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a deeplink for panel 5 in dashboard with UID 'monitoring-dash'"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {
            "resourceType": "panel",
            "uid": "panel-uid",
            "dashboardUid": "monitoring-dash",
            "panelId": 5
        }
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    panel_link_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain a valid Grafana panel deeplink URL with viewPanel parameter?",
        )
    )
    print("Panel deeplink content:", content)
    expect(input=prompt, output=content).to_pass(panel_link_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_explore_deeplink(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a deeplink for Grafana Explore with datasource 'prometheus-uid'"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {"resourceType": "explore", "uid": "prometheus-uid"}
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    explore_link_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain a valid Grafana Explore deeplink URL with /explore path and datasource parameter?",
        )
    )
    print("Explore deeplink content:", content)
    expect(input=prompt, output=content).to_pass(explore_link_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_time_range(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a dashboard deeplink for 'system-metrics' showing the last 6 hours"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {
            "resourceType": "dashboard",
            "uid": "system-metrics",
            "timeRange": {
                "from": "now-6h",
                "to": "now"
            }
        }
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    time_range_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain a Grafana deeplink URL with time range parameters (from and to query parameters)?",
        )
    )
    print("Time range deeplink content:", content)
    expect(input=prompt, output=content).to_pass(time_range_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_generate_deeplink_with_custom_params(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Generate a dashboard deeplink for 'app-metrics' with custom variables"

    messages = [
        Message(role="system", content="You are a helpful assistant."),
        Message(role="user", content=prompt),
    ]

    messages = await llm_tool_call_sequence(
        model, messages, tools, mcp_client, "generate_deeplink",
        {
            "resourceType": "dashboard",
            "uid": "app-metrics",
            "queryParams": {
                "var-datasource": "prometheus",
                "var-environment": "production",
                "refresh": "30s"
            }
        }
    )

    response = await acompletion(model=model, messages=messages, tools=tools)
    content = response.choices[0].message.content
    
    custom_params_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response contain a Grafana deeplink URL with custom query parameters like var-datasource or refresh?",
        )
    )
    print("Custom params deeplink content:", content)
    expect(input=prompt, output=content).to_pass(custom_params_checker)


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(max_runs=3)
async def test_navigation_tool_workflow(model: str, mcp_client: ClientSession):
    tools = await get_converted_tools(mcp_client)
    prompt = "Find a dashboard and then generate a deeplink to it with a 1-hour time range"

    messages = [
        Message(role="system", content="You are a helpful assistant that can search for Grafana dashboards and generate deeplinks."),
        Message(role="user", content=prompt),
    ]

    response = await acompletion(model=model, messages=messages, tools=tools)
    final_content = response.choices[0].message.content
    
    workflow_checker = CustomLLMBooleanEvaluator(
        settings=CustomLLMBooleanSettings(
            prompt="Does the response demonstrate finding dashboards and generating deeplinks with time range parameters?",
        )
    )
    print("Navigation workflow content:", final_content)
    expect(input=prompt, output=final_content).to_pass(workflow_checker)