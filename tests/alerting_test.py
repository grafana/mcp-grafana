import json

import pytest
from deepeval.test_case import MCPToolCall
from mcp import ClientSession
from typing import List, Optional

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop

pytestmark = pytest.mark.anyio


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_list_alert_rules(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list all alert rules using the alerting_manage_rules tool."""
    prompt = "List all alert rules in Grafana."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_rules", "list")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response list alert rules with their titles, states, and labels? "
        "There should be at least two rules including 'Test Alert Rule 1' and 'Test Alert Rule 2'.",
        expected_tools="alerting_manage_rules",
    )


def assert_tool_operation(
    tools_called: List[MCPToolCall],
    tool_name: str,
    operation: str,
    extra_args: Optional[dict] = None,
) -> MCPToolCall:
    """Assert a tool was called with the expected operation and return the matching call."""
    calls = [tc for tc in tools_called if tc.name == tool_name]
    assert calls, f"{tool_name} was not called"

    op_calls = [tc for tc in calls if tc.args.get("operation") == operation]
    assert op_calls, (
        f"Expected a '{operation}' operation call on {tool_name}. Operations called: "
        f"{[tc.args.get('operation') for tc in calls]}"
    )

    if extra_args:
        call = op_calls[0]
        for key, expected in extra_args.items():
            actual = call.args.get(key)
            assert actual == expected, f"Expected {key}={expected!r}, got {actual!r}"

    return op_calls[0]


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_get_alert_rule_by_uid(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can retrieve a specific alert rule's details by UID."""
    prompt = "Get the details of the alert rule with UID 'test_alert_rule_1'. "
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(
        tools_called,
        "alerting_manage_rules",
        "get",
        extra_args={"rule_uid": "test_alert_rule_1"},
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain detailed configuration of the alert rule, "
        "including its title ('Test Alert Rule 1'), queries, condition, and state?",
        expected_tools="alerting_manage_rules",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_list_alert_rules_with_label_filter(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can filter alert rules by label selectors."""
    prompt = "Show me all alert rules that have the label rule=first."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_rules", "list")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response show filtered alert rules? It should include "
        "'Test Alert Rule 1' (which has label rule=first) and should NOT include "
        "'Test Alert Rule 2' (which has label rule=second).",
        expected_tools="alerting_manage_rules",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_find_firing_alert_rules(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can identify currently firing alert rules."""
    prompt = "Show me all firing alert rules in Grafana"
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_rules", "list")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response list only the firing alert rules? "
        "It should include 'Test Alert Rule 1' which is firing, "
        "and should not list rules that are not firing.",
        expected_tools="alerting_manage_rules",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_get_alert_rule_versions(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can retrieve the version history of an alert rule."""
    prompt = (
        "Show me the version history for the alert rule with UID 'test_alert_rule_1'."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(
        tools_called,
        "alerting_manage_rules",
        "versions",
        extra_args={"rule_uid": "test_alert_rule_1"},
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain version history information for the alert rule?",
        expected_tools="alerting_manage_rules",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_list_alert_rules_in_folder(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list alert rules filtered by folder."""
    prompt = "What alert rules are in the 'Test Alerts' folder?"
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_rules", "list")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response list alert rules from the 'Test Alerts' folder? "
        "It should include 'Test Alert Rule 1' and 'Test Alert Rule 2'.",
        expected_tools="alerting_manage_rules",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_get_notification_policies(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can retrieve the notification policy tree."""
    prompt = "How are alerts routed to receivers in my Grafana instance?"
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(
        tools_called, "alerting_manage_routing", "get_notification_policies"
    )

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response describe the notification policy routing tree? "
        "It should mention that alerts with severity=info are routed to Email1, "
        "and that the 'weekends' mute time interval is applied.",
        expected_tools="alerting_manage_routing",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_list_contact_points(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list all contact points."""
    prompt = "List all contact points configured in Grafana alerting."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_routing", "get_contact_points")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response list contact points? "
        "It should include 'Email1' and 'Email2'.",
        expected_tools="alerting_manage_routing",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_get_contact_point_by_name(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can retrieve a specific contact point by name."""
    prompt = (
        "Show me the details of the contact point named 'Email1' in Grafana alerting."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_routing", "get_contact_point")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response contain details about the 'Email1' contact point, "
        "including that it is an email type sending to test1@example.com?",
        expected_tools="alerting_manage_routing",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_list_time_intervals(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can list all mute time intervals."""
    prompt = "Show me all mute time intervals configured in Grafana alerting."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_routing", "get_time_intervals")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response list time intervals? "
        "It should include a 'weekends' interval covering Saturday and Sunday.",
        expected_tools="alerting_manage_routing",
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_get_time_interval_by_name(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can retrieve a specific time interval by name."""
    prompt = (
        "Show me the details of the 'weekends' mute time interval in Grafana alerting."
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_routing", "get_time_interval")

    assert_mcp_eval(
        prompt,
        final_content,
        tools_called,
        mcp_server,
        "Does the response describe the 'weekends' time interval, "
        "including that it covers Saturday and Sunday?",
        expected_tools="alerting_manage_routing",
    )


@pytest.fixture
async def alert_rule_for_tests(mcp_client: ClientSession):
    """Create a non-provisioned alert rule for write tests, clean up afterwards."""
    rule_uid = "python-e2e-delete-test"

    # If the rule exists, delete it first
    await mcp_client.call_tool(
        "alerting_manage_rules",
        {"operation": "delete", "rule_uid": rule_uid},
    )

    result = await mcp_client.call_tool(
        "alerting_manage_rules",
        {
            "operation": "create",
            "rule_uid": rule_uid,
            "title": "rule-to-delete",
            "rule_group": "e2e-test-group",
            "folder_uid": "tests",
            "condition": "A",
            "data": [
                {
                    "refId": "A",
                    "queryType": "",
                    "relativeTimeRange": {"from": 600, "to": 0},
                    "datasourceUid": "prometheus",
                    "model": {
                        "expr": "vector(1)",
                        "refId": "A",
                    },
                },
            ],
            "no_data_state": "OK",
            "exec_err_state": "OK",
            "for": "5m",
            "org_id": 1,
            "labels": {"team": "python-e2e"},
        },
    )
    assert not result.isError, f"Failed to create test rule: {result.content}"

    yield rule_uid

    # Cleanup: delete if still exists
    await mcp_client.call_tool(
        "alerting_manage_rules",
        {"operation": "delete", "rule_uid": rule_uid},
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_delete_alert_rule(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
    alert_rule_for_tests: str,
):
    """Test that the LLM can delete an alert rule by UID."""
    rule_uid = alert_rule_for_tests

    prompt = f"Delete the alert rule with UID '{rule_uid}'."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(
        tools_called,
        "alerting_manage_rules",
        "delete",
        extra_args={"rule_uid": rule_uid},
    )

    # Verify the rule was actually deleted by listing all rules
    list_result = await mcp_client.call_tool(
        "alerting_manage_rules",
        {"operation": "list"},
    )
    rules = json.loads(list_result.content[0].text)
    rule_uids = [r["uid"] for r in rules]
    assert (
        rule_uid not in rule_uids
    ), f"Rule {rule_uid} should have been deleted but still appears in list"


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_create_alert_rule(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
):
    """Test that the LLM can create a new alert rule."""
    rule_title = "Python E2E Created Rule"
    prompt = (
        f"Create a Grafana alert rule titled '{rule_title}' in folder 'tests', "
        "rule group 'e2e-test-group', org ID 1. "
        "It should query the prometheus datasource with expression vector(1), "
        "fire when the value is above 0, with a 5m pending period. "
    )
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(tools_called, "alerting_manage_rules", "create")

    # Verify the rule was actually created
    list_result = await mcp_client.call_tool(
        "alerting_manage_rules",
        {"operation": "list"},
    )
    rules = json.loads(list_result.content[0].text)
    created = [r for r in rules if r["title"] == rule_title]
    assert created, f"Rule '{rule_title}' was not found after creation"

    # Cleanup
    await mcp_client.call_tool(
        "alerting_manage_rules",
        {"operation": "delete", "rule_uid": created[0]["uid"]},
    )


@pytest.mark.parametrize("model", models)
@pytest.mark.flaky(reruns=2)
async def test_update_alert_rule(
    model: str,
    mcp_client: ClientSession,
    mcp_transport: str,
    alert_rule_for_tests: str,
):
    """Test that the LLM can update an existing alert rule."""
    rule_uid = alert_rule_for_tests
    new_title = "Updated By LLM"

    prompt = f"Update the alert rule with UID '{rule_uid}': change its title to '{new_title}'."
    final_content, tools_called, mcp_server = await run_llm_tool_loop(
        model, mcp_client, mcp_transport, prompt
    )

    assert_tool_operation(
        tools_called,
        "alerting_manage_rules",
        "update",
        extra_args={"rule_uid": rule_uid},
    )

    # Verify the title was actually updated
    get_result = await mcp_client.call_tool(
        "alerting_manage_rules",
        {"operation": "get", "rule_uid": rule_uid},
    )
    rule = json.loads(get_result.content[0].text)
    assert (
        rule["title"] == new_title
    ), f"Expected title '{new_title}', got '{rule['title']}'"
