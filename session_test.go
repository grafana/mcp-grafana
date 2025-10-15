//go:build integration

// Integration tests for proxied MCP tools functionality.
// Requires docker-compose to be running with Grafana and Tempo instances.
// Run with: go test -tags integration -v ./...

package mcpgrafana

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-openapi/strfmt"
	grafana_client "github.com/grafana/grafana-openapi-client-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newProxiedToolsTestContext creates a test context with Grafana client and config
func newProxiedToolsTestContext(t *testing.T) context.Context {
	cfg := grafana_client.DefaultTransportConfig()
	cfg.Host = "localhost:3000"
	cfg.Schemes = []string{"http"}

	// Extract transport config from env vars, and set it on the context.
	if u, ok := os.LookupEnv("GRAFANA_URL"); ok {
		parsedURL, err := url.Parse(u)
		require.NoError(t, err, "invalid GRAFANA_URL")
		cfg.Host = parsedURL.Host
		// The Grafana client will always prefer HTTPS even if the URL is HTTP,
		// so we need to limit the schemes to HTTP if the URL is HTTP.
		if parsedURL.Scheme == "http" {
			cfg.Schemes = []string{"http"}
		}
	}

	// Check for the new service account token environment variable first
	if apiKey := os.Getenv("GRAFANA_SERVICE_ACCOUNT_TOKEN"); apiKey != "" {
		cfg.APIKey = apiKey
	} else if apiKey := os.Getenv("GRAFANA_API_KEY"); apiKey != "" {
		// Fall back to the deprecated API key environment variable
		cfg.APIKey = apiKey
	} else {
		cfg.BasicAuth = url.UserPassword("admin", "admin")
	}

	grafanaClient := grafana_client.NewHTTPClientWithConfig(strfmt.Default, cfg)

	grafanaCfg := GrafanaConfig{
		Debug:     true,
		URL:       "http://localhost:3000",
		APIKey:    cfg.APIKey,
		BasicAuth: cfg.BasicAuth,
	}

	ctx := WithGrafanaConfig(context.Background(), grafanaCfg)
	return WithGrafanaClient(ctx, grafanaClient)
}

func TestDiscoverMCPDatasources(t *testing.T) {
	ctx := newProxiedToolsTestContext(t)

	t.Run("discovers tempo datasources", func(t *testing.T) {
		discovered, err := discoverMCPDatasources(ctx)
		require.NoError(t, err)

		// Should find two Tempo datasources from docker-compose
		assert.GreaterOrEqual(t, len(discovered), 2, "Should discover at least 2 Tempo datasources")

		// Check that we found the expected datasources
		uids := make([]string, len(discovered))
		for i, ds := range discovered {
			uids[i] = ds.UID
			assert.Equal(t, "tempo", ds.Type, "All discovered datasources should be tempo type")
			assert.NotEmpty(t, ds.Name, "Datasource should have a name")
			assert.NotEmpty(t, ds.MCPURL, "Datasource should have MCP URL")

			// Verify URL format
			expectedURLPattern := fmt.Sprintf("http://localhost:3000/api/datasources/proxy/uid/%s/api/mcp", ds.UID)
			assert.Equal(t, expectedURLPattern, ds.MCPURL, "MCP URL should follow proxy pattern")
		}

		// Should contain our expected UIDs
		assert.Contains(t, uids, "tempo", "Should discover 'tempo' datasource")
		assert.Contains(t, uids, "tempo-secondary", "Should discover 'tempo-secondary' datasource")
	})

	t.Run("returns error when grafana client not in context", func(t *testing.T) {
		emptyCtx := context.Background()
		discovered, err := discoverMCPDatasources(emptyCtx)
		assert.Error(t, err)
		assert.Nil(t, discovered)
		assert.Contains(t, err.Error(), "grafana client not found in context")
	})

	t.Run("returns error when auth is missing", func(t *testing.T) {
		// Context with client but no auth credentials
		cfg := grafana_client.DefaultTransportConfig()
		cfg.Host = "localhost:3000"
		cfg.Schemes = []string{"http"}
		grafanaClient := grafana_client.NewHTTPClientWithConfig(strfmt.Default, cfg)

		grafanaCfg := GrafanaConfig{
			URL: "http://localhost:3000",
			// No APIKey or BasicAuth set
		}
		ctx := WithGrafanaConfig(context.Background(), grafanaCfg)
		ctx = WithGrafanaClient(ctx, grafanaClient)

		discovered, err := discoverMCPDatasources(ctx)
		assert.Error(t, err)
		assert.Nil(t, discovered)
		assert.Contains(t, err.Error(), "Unauthorized")
	})
}

func TestToolNamespacing(t *testing.T) {
	t.Run("parse proxied tool name", func(t *testing.T) {
		datasourceType, toolName, err := parseProxiedToolName("tempo_traceql-search")
		require.NoError(t, err)
		assert.Equal(t, "tempo", datasourceType)
		assert.Equal(t, "traceql-search", toolName)
	})

	t.Run("parse proxied tool name with multiple underscores", func(t *testing.T) {
		datasourceType, toolName, err := parseProxiedToolName("tempo_get-attribute-values")
		require.NoError(t, err)
		assert.Equal(t, "tempo", datasourceType)
		assert.Equal(t, "get-attribute-values", toolName)
	})

	t.Run("parse proxied tool name with invalid format", func(t *testing.T) {
		_, _, err := parseProxiedToolName("invalid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid proxied tool name format")
	})

	t.Run("add datasourceUid parameter to tool", func(t *testing.T) {
		originalTool := mcp.Tool{
			Name:        "query_traces",
			Description: "Query traces from Tempo",
			InputSchema: mcp.ToolInputSchema{
				Properties: map[string]any{
					"query": map[string]any{
						"type": "string",
					},
				},
				Required: []string{"query"},
			},
		}

		modifiedTool := addDatasourceUidParameter(originalTool, "tempo")

		assert.Equal(t, "tempo_query_traces", modifiedTool.Name)
		assert.Equal(t, "Query traces from Tempo", modifiedTool.Description)
		assert.NotNil(t, modifiedTool.InputSchema.Properties["datasourceUid"])
		assert.Contains(t, modifiedTool.InputSchema.Required, "datasourceUid")
		assert.Contains(t, modifiedTool.InputSchema.Required, "query")
	})

	t.Run("add datasourceUid parameter with empty description", func(t *testing.T) {
		originalTool := mcp.Tool{
			Name:        "test_tool",
			Description: "",
			InputSchema: mcp.ToolInputSchema{
				Properties: make(map[string]any),
			},
		}

		modifiedTool := addDatasourceUidParameter(originalTool, "tempo")

		assert.Equal(t, "tempo_test_tool", modifiedTool.Name)
		assert.Equal(t, "", modifiedTool.Description, "Should not modify empty description")
		assert.NotNil(t, modifiedTool.InputSchema.Properties["datasourceUid"])
	})
}

func TestSessionStateLifecycle(t *testing.T) {
	t.Run("create and get session", func(t *testing.T) {
		sm := NewSessionManager()

		// Create mock session
		mockSession := &mockClientSession{id: "test-session-123"}

		sm.CreateSession(context.Background(), mockSession)

		state, exists := sm.GetSession("test-session-123")
		assert.True(t, exists)
		assert.NotNil(t, state)
		assert.NotNil(t, state.proxiedClients)
		assert.False(t, state.proxiedToolsInitialized)
	})

	t.Run("remove session cleans up clients", func(t *testing.T) {
		sm := NewSessionManager()

		mockSession := &mockClientSession{id: "test-session-456"}
		sm.CreateSession(context.Background(), mockSession)

		state, _ := sm.GetSession("test-session-456")

		// Add a mock proxied client
		mockClient := &ProxiedClient{
			DatasourceUID:  "test-uid",
			DatasourceName: "Test Datasource",
			DatasourceType: "tempo",
		}
		state.proxiedClients["tempo_test-uid"] = mockClient

		// Remove session
		sm.RemoveSession(context.Background(), mockSession)

		// Session should be gone
		_, exists := sm.GetSession("test-session-456")
		assert.False(t, exists)
	})

	t.Run("get non-existent session", func(t *testing.T) {
		sm := NewSessionManager()

		state, exists := sm.GetSession("non-existent")
		assert.False(t, exists)
		assert.Nil(t, state)
	})
}

func TestProxiedClientLifecycle(t *testing.T) {
	ctx := newProxiedToolsTestContext(t)

	t.Run("list tools returns copy", func(t *testing.T) {
		pc := &ProxiedClient{
			DatasourceUID:  "test-uid",
			DatasourceName: "Test",
			DatasourceType: "tempo",
			Tools: []mcp.Tool{
				{Name: "tool1", Description: "First tool"},
				{Name: "tool2", Description: "Second tool"},
			},
		}

		tools1 := pc.ListTools()
		tools2 := pc.ListTools()

		// Should return same content
		assert.Equal(t, tools1, tools2)

		// But different slice instances (copy)
		assert.NotSame(t, &tools1[0], &tools2[0])
	})

	t.Run("call tool validates tool exists", func(t *testing.T) {
		pc := &ProxiedClient{
			DatasourceUID:  "test-uid",
			DatasourceName: "Test",
			DatasourceType: "tempo",
			Tools: []mcp.Tool{
				{Name: "valid_tool", Description: "Valid tool"},
			},
		}

		// Call non-existent tool
		result, err := pc.CallTool(ctx, "non_existent_tool", map[string]any{})
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "not found in remote MCP server")
	})
}

func TestEndToEndProxiedToolsFlow(t *testing.T) {
	ctx := newProxiedToolsTestContext(t)

	t.Run("full flow from discovery to tool call", func(t *testing.T) {
		// Step 1: Discover MCP datasources
		discovered, err := discoverMCPDatasources(ctx)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(discovered), 1, "Should discover at least one Tempo datasource")

		// Use the first discovered datasource
		ds := discovered[0]
		t.Logf("Testing with datasource: %s (UID: %s, URL: %s)", ds.Name, ds.UID, ds.MCPURL)

		// Step 2: Create a proxied client connection
		client, err := NewProxiedClient(ctx, ds.UID, ds.Name, ds.Type, ds.MCPURL)
		if err != nil {
			t.Skipf("Skipping end-to-end test: Tempo MCP endpoint not available: %v", err)
			return
		}
		defer func() {
			_ = client.Close()
		}()

		// Step 3: Verify we got tools from the remote server
		tools := client.ListTools()
		require.Greater(t, len(tools), 0, "Should have at least one tool from Tempo MCP server")
		t.Logf("Discovered %d tools from Tempo MCP server", len(tools))

		// Log the available tools
		for _, tool := range tools {
			t.Logf("  - Tool: %s - %s", tool.Name, tool.Description)
		}

		// Step 4: Test tool modification with datasourceUid parameter
		firstTool := tools[0]
		modifiedTool := addDatasourceUidParameter(firstTool, ds.Type)

		expectedName := ds.Type + "_" + firstTool.Name
		assert.Equal(t, expectedName, modifiedTool.Name, "Modified tool should have prefixed name")
		assert.Contains(t, modifiedTool.InputSchema.Required, "datasourceUid", "Modified tool should require datasourceUid")

		// Step 5: Test session integration
		sm := NewSessionManager()
		mockSession := &mockClientSession{id: "e2e-test-session"}
		sm.CreateSession(ctx, mockSession)

		state, exists := sm.GetSession("e2e-test-session")
		require.True(t, exists)

		// Store the proxied client in session state
		key := ds.Type + "_" + ds.UID
		state.proxiedClients[key] = client

		// Step 6: Verify client is stored correctly in session
		retrievedClient, exists := state.proxiedClients[key]
		require.True(t, exists, "Client should be stored in session state")
		assert.Equal(t, client, retrievedClient, "Should retrieve the same client from session")

		// Step 7: Test ProxiedToolHandler flow
		handler := NewProxiedToolHandler(sm, nil, modifiedTool.Name)
		assert.NotNil(t, handler)

		// Note: We can't actually call the tool without knowing what arguments it expects
		// and without the context having the proper session, but we've validated the setup
		t.Logf("Successfully validated end-to-end proxied tools flow")
	})

	t.Run("multiple datasources in single session", func(t *testing.T) {
		discovered, err := discoverMCPDatasources(ctx)
		require.NoError(t, err)

		if len(discovered) < 2 {
			t.Skip("Need at least 2 Tempo datasources for this test")
		}

		sm := NewSessionManager()
		mockSession := &mockClientSession{id: "multi-ds-test-session"}
		sm.CreateSession(ctx, mockSession)

		state, _ := sm.GetSession("multi-ds-test-session")

		// Try to connect to multiple datasources
		connectedCount := 0
		for i, ds := range discovered {
			if i >= 2 {
				break // Test with first 2 datasources
			}

			client, err := NewProxiedClient(ctx, ds.UID, ds.Name, ds.Type, ds.MCPURL)
			if err != nil {
				t.Logf("Could not connect to datasource %s: %v", ds.UID, err)
				continue
			}
			defer func() {
				_ = client.Close()
			}()

			key := ds.Type + "_" + ds.UID
			state.proxiedClients[key] = client
			connectedCount++

			t.Logf("Connected to datasource %s with %d tools", ds.UID, len(client.Tools))
		}

		if connectedCount == 0 {
			t.Skip("Could not connect to any Tempo datasources")
		}

		// Verify each client is stored correctly
		for key, client := range state.proxiedClients {
			parts := strings.Split(key, "_")
			require.Len(t, parts, 2, "Key should have format type_uid")
			assert.NotNil(t, client, "Client should not be nil")
			assert.Equal(t, parts[0], client.DatasourceType, "Client type should match key")
			assert.Equal(t, parts[1], client.DatasourceUID, "Client UID should match key")
		}

		t.Logf("Successfully managed %d datasources in single session", connectedCount)
	})
}

// mockClientSession implements server.ClientSession for testing
type mockClientSession struct {
	id            string
	notifChannel  chan mcp.JSONRPCNotification
	isInitialized bool
}

func (m *mockClientSession) SessionID() string {
	return m.id
}

func (m *mockClientSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	if m.notifChannel == nil {
		m.notifChannel = make(chan mcp.JSONRPCNotification, 10)
	}
	return m.notifChannel
}

func (m *mockClientSession) Initialize() {
	m.isInitialized = true
}

func (m *mockClientSession) Initialized() bool {
	return m.isInitialized
}
