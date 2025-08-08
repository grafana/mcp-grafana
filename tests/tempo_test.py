import pytest


class TestTempoMCPProxy:
    """Test Tempo MCP proxy functionality.
    
    Note: These tests run with GRAFANA_URL environment variable set, which triggers
    automatic discovery of Tempo datasources and registration of their tools at
    server startup. If no Tempo datasources exist or GRAFANA_URL is not set,
    no Tempo tools will be registered.
    """

    @pytest.mark.anyio
    async def test_tempo_tools_discovery_and_proxying(self, mcp_client):
        """Test that Tempo tools are discovered, wrapped, and properly proxied through our MCP server."""
        
        # Step 1: Verify all expected Tempo tools are discovered and registered
        list_response = await mcp_client.list_tools()
        all_tool_names = [tool.name for tool in list_response.tools]
        
        # Look for tempo-prefixed tools
        tempo_tools = [name for name in all_tool_names if name.startswith("tempo_")]
        expected_tempo_tools = [
            "tempo_traceql_search",
            "tempo_traceql_metrics_instant", 
            "tempo_traceql_metrics_range",
            "tempo_get_trace",
            "tempo_get_attribute_names",
            "tempo_get_attribute_values",
            "tempo_docs_traceql"
        ]
        
        assert len(tempo_tools) == len(expected_tempo_tools), f"Expected {len(expected_tempo_tools)} tempo tools, found {len(tempo_tools)}: {tempo_tools}"
        
        for expected_tool in expected_tempo_tools:
            assert expected_tool in tempo_tools, f"Proxied tool {expected_tool} should be available"
        
        # Step 2: Verify tool schemas include the required datasource_uid parameter
        tempo_tool_objects = [tool for tool in list_response.tools if tool.name.startswith("tempo_")]
        
        for tool in tempo_tool_objects:
            # Verify the tool has been wrapped with datasource_uid
            assert isinstance(tool.inputSchema, dict), f"Tool {tool.name} should have input schema as dict"
            assert 'properties' in tool.inputSchema, f"Tool {tool.name} should have properties in input schema"
            
            properties = tool.inputSchema.get('properties', {})
            assert 'datasource_uid' in properties, f"Tool {tool.name} should require datasource_uid parameter"
            
            # Verify required fields include datasource_uid
            required = tool.inputSchema.get('required', [])
            assert 'datasource_uid' in required, f"Tool {tool.name} should require datasource_uid"

    @pytest.mark.anyio
    @pytest.mark.parametrize("tool,args,expected_response_indicators", [
        (
            "tempo_get_attribute_names",
            {"datasource_uid": "tempo"},
            ["tempo", "datasource"]
        ),
        (
            "tempo_docs_traceql",
            {"datasource_uid": "tempo"},
            ["tempo", "traceql"]
        )
    ])
    async def test_tempo_tool_calls_through_proxy(self, mcp_client, tool, args, expected_response_indicators):
        """Test that tempo tool calls are properly routed through our proxy."""
        
        # Call the proxied tool
        call_response = await mcp_client.call_tool(tool, arguments=args)
        
        # Verify we got a response
        assert call_response.content, f"Tool {tool} should return content"
        response_text = call_response.content[0].text
        
        # Verify the response indicates it went through our proxy
        # Our current implementation returns mock responses, so verify that
        assert "Proxied call" in response_text, f"Response should indicate proxy routing: {response_text}"
        assert args["datasource_uid"] in response_text, f"Response should include datasource UID: {response_text}"
        
        # Verify expected content indicators
        for indicator in expected_response_indicators:
            assert indicator.lower() in response_text.lower(), f"Response should contain '{indicator}': {response_text}"

    @pytest.mark.anyio
    async def test_tempo_tool_validation(self, mcp_client):
        """Test that tempo tools properly validate required parameters."""
        
        # Test missing datasource_uid should fail
        with pytest.raises(Exception) as exc_info:
            await mcp_client.call_tool(
                "tempo_get_attribute_names",
                arguments={}  # Missing datasource_uid
            )
        
        assert "datasource_uid is required" in str(exc_info.value).lower()
        
        # Test invalid datasource_uid should fail appropriately  
        with pytest.raises(Exception) as exc_info:
            await mcp_client.call_tool(
                "tempo_get_attribute_names", 
                arguments={"datasource_uid": "nonexistent"}
            )
        
        # Should fail with datasource not found or similar error
        error_msg = str(exc_info.value).lower()
        assert any(phrase in error_msg for phrase in ["not found", "invalid", "nonexistent"]), f"Should reject invalid datasource: {error_msg}"

    @pytest.mark.anyio
    async def test_tool_name_normalization(self, mcp_client):
        """Test that tool names are properly normalized (hyphens to underscores)."""
        
        list_response = await mcp_client.list_tools()
        tempo_tools = [tool.name for tool in list_response.tools if tool.name.startswith("tempo_")]
        
        # Verify original hyphenated names are converted to underscores
        original_to_normalized = {
            "traceql-search": "tempo_traceql_search",
            "traceql-metrics-instant": "tempo_traceql_metrics_instant", 
            "traceql-metrics-range": "tempo_traceql_metrics_range",
            "get-trace": "tempo_get_trace",
            "get-attribute-names": "tempo_get_attribute_names",
            "get-attribute-values": "tempo_get_attribute_values",
            "docs-traceql": "tempo_docs_traceql"
        }
        
        for normalized_name in original_to_normalized.values():
            assert normalized_name in tempo_tools, f"Normalized tool name {normalized_name} should be available"
            
        # Verify no hyphenated tempo tools exist (they should all be normalized)
        hyphenated_tempo_tools = [name for name in tempo_tools if "-" in name]
        assert len(hyphenated_tempo_tools) == 0, f"No hyphenated tempo tools should exist: {hyphenated_tempo_tools}" 
