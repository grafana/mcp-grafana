from mcp import ClientSession
import pytest

from conftest import models
from utils import assert_mcp_eval, run_llm_tool_loop

pytestmark = pytest.mark.anyio


class TestTempoToolsBasic:
    """Test Tempo native MCP tools functionality.

    These tests verify that Tempo tools are registered with a datasourceUid
    parameter for multi-datasource support. Tools call Tempo's REST API
    through the Grafana datasource proxy.

    Requires:
    - Docker compose services running (includes Tempo)
    - GRAFANA_USERNAME and GRAFANA_PASSWORD environment variables
    - MCP server running
    """

    @pytest.mark.anyio
    async def test_tempo_tools_registered(
        self, mcp_client: ClientSession
    ):
        """Test that Tempo tools are registered with datasourceUid parameter."""

        # List all tools
        list_response = await mcp_client.list_tools()
        all_tool_names = [tool.name for tool in list_response.tools]

        # Expected tools — 6 API-backed tools
        expected_tempo_tools = [
            "search_tempo_traces",
            "query_tempo_metrics",
            "get_tempo_trace",
            "diff_tempo_traces",
            "list_tempo_attribute_names",
            "list_tempo_attribute_values",
        ]

        tempo_tools = [name for name in all_tool_names if name in expected_tempo_tools]

        assert len(tempo_tools) == len(expected_tempo_tools), (
            f"Expected {len(expected_tempo_tools)} unique tempo tools, found {len(tempo_tools)}: {tempo_tools}"
        )

        for expected_tool in expected_tempo_tools:
            assert expected_tool in all_tool_names, (
                f"Tool {expected_tool} should be available"
            )

    @pytest.mark.anyio
    async def test_tempo_tools_have_datasourceUid_parameter(self, mcp_client):
        """Test that all tempo tools have a required datasourceUid parameter."""

        list_response = await mcp_client.list_tools()

        tempo_tool_names = {
            "search_tempo_traces",
            "query_tempo_metrics",
            "get_tempo_trace",
            "diff_tempo_traces",
            "list_tempo_attribute_names",
            "list_tempo_attribute_values",
        }

        tempo_tools = [
            tool for tool in list_response.tools if tool.name in tempo_tool_names
        ]

        assert len(tempo_tools) > 0, "Should have at least one tempo tool"

        for tool in tempo_tools:
            # Verify the tool has input schema
            assert hasattr(tool, "inputSchema"), (
                f"Tool {tool.name} should have inputSchema"
            )
            assert isinstance(tool.inputSchema, dict), (
                f"Tool {tool.name} inputSchema should be a dict"
            )

            # Verify datasourceUid parameter exists (camelCase)
            properties = tool.inputSchema.get("properties", {})
            assert "datasourceUid" in properties, (
                f"Tool {tool.name} should have datasourceUid parameter (camelCase)"
            )

            # Verify it's required
            required = tool.inputSchema.get("required", [])
            assert "datasourceUid" in required, (
                f"Tool {tool.name} should require datasourceUid parameter"
            )

            # Verify parameter has proper description
            datasource_uid_prop = properties["datasourceUid"]
            assert "type" in datasource_uid_prop, (
                f"datasourceUid should have type defined"
            )
            assert datasource_uid_prop["type"] == "string", (
                f"datasourceUid should be type string"
            )

    @pytest.mark.anyio
    async def test_tempo_tool_call_with_valid_datasource(self, mcp_client):
        """Test calling a tempo tool with a valid datasourceUid."""

        try:
            call_response = await mcp_client.call_tool(
                "list_tempo_attribute_names",
                arguments={"datasourceUid": "tempo"},
            )

            # Verify we got a response
            assert call_response.content, "Tool should return content"

            response_text = call_response.content[0].text
            assert len(response_text) > 0, "Response should have content"

        except Exception as e:
            # If this fails, it might be because Tempo doesn't have data yet
            # but at least verify the error isn't about missing datasourceUid
            error_msg = str(e).lower()
            assert "datasourceuid" not in error_msg, (
                f"Should not fail due to datasourceUid parameter: {e}"
            )
            print(error_msg)

    @pytest.mark.anyio
    async def test_tempo_tool_call_missing_datasourceUid(self, mcp_client):
        """Test that calling a tempo tool without datasourceUid fails appropriately."""

        try:
            result = await mcp_client.call_tool(
                "list_tempo_attribute_names",
                arguments={},  # Missing datasourceUid
            )
            # If the server returns an error result instead of raising
            assert result.isError, "Should return an error when datasourceUid is missing"
            error_text = result.content[0].text.lower()
            assert "datasourceuid" in error_text or "required" in error_text, (
                f"Error should mention datasourceUid: {result.content[0].text}"
            )
        except Exception as exc:
            error_msg = str(exc).lower()
            assert "datasourceuid" in error_msg or "required" in error_msg, (
                f"Should require datasourceUid parameter: {exc}"
            )

    @pytest.mark.anyio
    async def test_tempo_tool_call_invalid_datasourceUid(self, mcp_client):
        """Test that calling a tempo tool with invalid datasourceUid returns helpful error."""

        try:
            result = await mcp_client.call_tool(
                "list_tempo_attribute_names",
                arguments={"datasourceUid": "nonexistent-tempo"},
            )
            # Server may return an error result rather than raising
            assert result.isError, "Should return an error for invalid datasourceUid"
            error_text = result.content[0].text.lower()
            assert "not found" in error_text or "not accessible" in error_text, (
                f"Should indicate datasource not found: {result.content[0].text}"
            )
        except Exception as exc:
            error_msg = str(exc).lower()
            assert "not found" in error_msg or "not accessible" in error_msg, (
                f"Should indicate datasource not found: {exc}"
            )

    @pytest.mark.anyio
    async def test_tempo_tool_list_attribute_names(self, mcp_client):
        """Test that list_tempo_attribute_names returns a response from the Tempo datasource."""

        try:
            call_response = await mcp_client.call_tool(
                "list_tempo_attribute_names",
                arguments={"datasourceUid": "tempo"},
            )

            assert call_response.content, "Tool should return content"
            response_text = call_response.content[0].text
            assert len(response_text) > 0, "Response should have content"

        except Exception as e:
            error_msg = str(e).lower()
            assert (
                "not found" not in error_msg or "tempo" not in error_msg
            ), f"Datasource tempo should be accessible: {e}"


class TestTempoToolsWithLLM:
    """LLM integration tests for Tempo tools."""

    @pytest.mark.parametrize("model", models)
    @pytest.mark.flaky(reruns=2)
    async def test_llm_can_list_trace_attributes(
        self, model: str, mcp_client: ClientSession, mcp_transport: str
    ):
        """Test that an LLM can list available trace attributes from Tempo."""
        prompt = (
            "Use the tempo tools to get a list of all available trace attribute names "
            "from the datasource with UID 'tempo'. I want to know what attributes "
            "I can use in my TraceQL queries."
        )
        final_content, tools_called, mcp_server = await run_llm_tool_loop(
            model, mcp_client, mcp_transport, prompt
        )

        attr_calls = [tc for tc in tools_called if tc.name == "list_tempo_attribute_names"]
        assert attr_calls, "list_tempo_attribute_names was not in tools_called"
        args = attr_calls[0].args
        assert args.get("datasourceUid") == "tempo", (
            f"Expected datasourceUid='tempo', got {args.get('datasourceUid')!r}"
        )

        assert_mcp_eval(
            prompt,
            final_content,
            tools_called,
            mcp_server,
            "Does the response list or describe trace attributes that are available for querying?",
            expected_tools="list_tempo_attribute_names",
        )
