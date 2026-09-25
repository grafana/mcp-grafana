import pytest
import os
from mcp.client.stdio import stdio_client
from mcp import ClientSession, StdioServerParameters

pytestmark = pytest.mark.anyio


@pytest.fixture
def grafana_env():
    env = {
        "GRAFANA_URL": os.environ.get("GRAFANA_URL", "http://localhost:3000"),
        "GRAFANA_USAGE_STATS": "disabled",
    }
    # Check for the new service account token environment variable first
    if key := os.environ.get("GRAFANA_SERVICE_ACCOUNT_TOKEN"):
        env["GRAFANA_SERVICE_ACCOUNT_TOKEN"] = key
    elif key := os.environ.get("GRAFANA_API_KEY"):
        env["GRAFANA_API_KEY"] = key
    return env


async def test_disable_write_flag_disables_write_tools(grafana_env):
    """Test that --disable-write flag disables write tools."""
    params = StdioServerParameters(
        command=os.environ.get("MCP_GRAFANA_PATH", "../dist/mcp-grafana"),
        args=["--disable-write"],
        env=grafana_env,
    )
    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()

            # List all available tools
            tools_result = await session.list_tools()
            tool_names = [tool.name for tool in tools_result.tools]

            # Verify write tools are NOT present
            write_tools = [
                "update_dashboard",
                "create_folder",
                "create_incident",
                "add_activity_to_incident",
                "update_incident",
                "create_annotation",
                "update_annotation",
                "delete_annotation",
                "update_alert_group",
                "alerting_rules_write",
                "alerting_silences_write",
            ]

            for tool in write_tools:
                assert tool not in tool_names, f"Write tool '{tool}' should not be available with --disable-write flag"

            # Verify the read-only alerting_rules_read is present
            assert "alerting_rules_read" in tool_names, "alerting_rules_read should be available with --disable-write flag"
            alerting_tool = next(t for t in tools_result.tools if t.name == "alerting_rules_read")
            assert alerting_tool.annotations.readOnlyHint is True, "alerting_rules_read should be read-only with --disable-write flag"

            # Verify the read-only alerting_silences_read is present
            assert "alerting_silences_read" in tool_names, "alerting_silences_read should be available with --disable-write flag"
            silences_tool = next(t for t in tools_result.tools if t.name == "alerting_silences_read")
            assert silences_tool.annotations.readOnlyHint is True, "alerting_silences_read should be read-only with --disable-write flag"

            # Verify read tools ARE still present
            read_tools = [
                "get_dashboard_by_uid",
                "alerting_rules_read",
                "alerting_silences_read",
                "alerting_manage_routing",
                "list_incidents",
                "get_incident",
                "list_incident_custom_fields",
                "get_annotations",
                "get_annotation_tags",
                "list_alert_groups",
                "get_alert_group",
            ]

            for tool in read_tools:
                assert tool in tool_names, f"Read tool '{tool}' should still be available with --disable-write flag"


async def test_without_disable_write_flag_enables_write_tools(grafana_env):
    """Test that without --disable-write flag, write tools are enabled."""
    params = StdioServerParameters(
        command=os.environ.get("MCP_GRAFANA_PATH", "../dist/mcp-grafana"),
        args=[],  # No --disable-write flag
        env=grafana_env,
    )
    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()

            # List all available tools
            tools_result = await session.list_tools()
            tool_names = [tool.name for tool in tools_result.tools]

            # Verify write tools ARE present
            write_tools = [
                "update_dashboard",
                "create_folder",
                "create_incident",
                "add_activity_to_incident",
                "update_incident",
                "create_annotation",
                "update_annotation",
                "delete_annotation",
                "update_alert_group",
                "alerting_rules_write",
                "alerting_silences_write",
            ]

            for tool in write_tools:
                assert tool in tool_names, f"Write tool '{tool}' should be available without --disable-write flag"

            # Verify the read-write alerting_rules_write is present (with destructive hint)
            alerting_tool = next(t for t in tools_result.tools if t.name == "alerting_rules_write")
            assert alerting_tool.annotations.destructiveHint is True, "alerting_rules_write should be marked destructive without --disable-write flag"

            # Verify read tools are also present
            read_tools = [
                "get_dashboard_by_uid",
                "alerting_rules_read",
                "alerting_silences_read",
                "alerting_rules_write",
                "alerting_silences_write",
                "alerting_manage_routing",
                "list_incidents",
                "get_incident",
                "list_incident_custom_fields",
                "get_annotations",
                "get_annotation_tags",
                "list_alert_groups",
                "get_alert_group",
            ]

            for tool in read_tools:
                assert tool in tool_names, f"Read tool '{tool}' should be available without --disable-write flag"
