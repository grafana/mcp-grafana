//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallMCP_ExtractsConfigFromContext(t *testing.T) {
	tests := []struct {
		name            string
		contextSetup    func() context.Context
		datasourceUID   string
		setupDatasource bool
		expectedError   string
		validateReq     func(t *testing.T, capturedBody []byte)
	}{
		{
			name: "successful config extraction with API key",
			contextSetup: func() context.Context {
				cfg := mcpgrafana.GrafanaConfig{
					URL:    "https://grafana.example.com",
					APIKey: "test-api-key",
				}
				return mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			},
			datasourceUID:   "test-uid",
			setupDatasource: true,
			validateReq: func(t *testing.T, capturedBody []byte) {
				var rpcReq JSONRPCRequest
				err := json.Unmarshal(capturedBody, &rpcReq)
				require.NoError(t, err)
				assert.Equal(t, "2.0", rpcReq.JSONRPC)
				assert.Equal(t, "test", rpcReq.Method)
			},
		},
		{
			name: "successful config extraction without API key",
			contextSetup: func() context.Context {
				cfg := mcpgrafana.GrafanaConfig{
					URL: "https://grafana.example.com",
				}
				return mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			},
			datasourceUID:   "test-uid",
			setupDatasource: true,
			validateReq: func(t *testing.T, capturedBody []byte) {
				var rpcReq JSONRPCRequest
				err := json.Unmarshal(capturedBody, &rpcReq)
				require.NoError(t, err)
			},
		},
		{
			name: "trailing slash in URL is handled correctly",
			contextSetup: func() context.Context {
				cfg := mcpgrafana.GrafanaConfig{
					URL:    "https://grafana.example.com/",
					APIKey: "test-api-key",
				}
				return mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			},
			datasourceUID:   "test-uid",
			setupDatasource: true,
			validateReq: func(t *testing.T, capturedBody []byte) {
				var rpcReq JSONRPCRequest
				err := json.Unmarshal(capturedBody, &rpcReq)
				require.NoError(t, err)
			},
		},
		{
			name: "error when URL not in context",
			contextSetup: func() context.Context {
				return context.Background()
			},
			datasourceUID: "test-uid",
			expectedError: "grafana URL not found in context",
		},
		{
			name: "error when datasource not found",
			contextSetup: func() context.Context {
				cfg := mcpgrafana.GrafanaConfig{
					URL:    "https://grafana.example.com",
					APIKey: "test-api-key",
				}
				return mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
			},
			datasourceUID: "nonexistent-uid",
			expectedError: "failed to get datasource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			var capturedReq *http.Request
			var capturedBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedReq = r
				
				// Read the entire request body
				body := make([]byte, r.ContentLength)
				_, err := r.Body.Read(body)
				if err != nil && err.Error() != "EOF" {
					t.Fatalf("failed to read request body: %v", err)
				}
				capturedBody = body
				
				// Return success response
				w.Header().Set("Content-Type", "application/json")
				response := JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      1,
					Result:  "success",
				}
				json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()

			// Setup datasource in cache if needed
			if tt.setupDatasource {
				datasourcesLock.Lock()
				if proxyDatasources["tempo"] == nil {
					proxyDatasources["tempo"] = make(map[string]ProxyDatasource)
				}
				proxyDatasources["tempo"][tt.datasourceUID] = ProxyDatasource{
					ID:   1,
					UID:  tt.datasourceUID,
					Name: "Test Datasource",
					URL:  server.URL,
					Type: "tempo",
				}
				datasourcesLock.Unlock()
			}

			// Override the context to use our test server URL
			ctx := tt.contextSetup()
			if cfg := mcpgrafana.GrafanaConfigFromContext(ctx); cfg.URL != "" {
				cfg.URL = server.URL
				ctx = mcpgrafana.WithGrafanaConfig(ctx, cfg)
			}

			// Call the function
			_, err := callMCP(ctx, tt.datasourceUID, "test", nil)

			// Validate
			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				
				// Validate URL construction
				expectedPath := "/api/datasources/proxy/1/api/mcp"
				assert.Equal(t, expectedPath, capturedReq.URL.Path)
				
				// Validate headers
				assert.Equal(t, "application/json", capturedReq.Header.Get("Content-Type"))
				
				if cfg := mcpgrafana.GrafanaConfigFromContext(ctx); cfg.APIKey != "" {
					assert.Equal(t, "Bearer "+cfg.APIKey, capturedReq.Header.Get("Authorization"))
				}
				
				// Run custom validation
				if tt.validateReq != nil {
					tt.validateReq(t, capturedBody)
				}
			}
		})
	}
}

func TestCallMCP_SessionIDHeader(t *testing.T) {
	// Test that session ID header is included when set
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify session ID header
		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID != "test-session-123" {
			t.Errorf("expected Mcp-Session-Id header 'test-session-123', got %q", sessionID)
		}
		
		response := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  map[string]interface{}{"success": true},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Setup context
	cfg := mcpgrafana.GrafanaConfig{
		URL: server.URL,
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)

	// Setup datasource
	datasourcesLock.Lock()
	if proxyDatasources["tempo"] == nil {
		proxyDatasources["tempo"] = make(map[string]ProxyDatasource)
	}
	proxyDatasources["tempo"]["test-uid"] = ProxyDatasource{
		ID:   1,
		UID:  "test-uid",
		Name: "Test",
		URL:  server.URL,
		Type: "tempo",
	}
	datasourcesLock.Unlock()

	// Set session ID
	sessionManager.SetSessionID("test-uid", "test-session-123")

	// Make request
	_, err := callMCP(ctx, "test-uid", "test", nil)
	require.NoError(t, err)
}

func TestSessionManager(t *testing.T) {
	t.Run("create and retrieve session", func(t *testing.T) {
		sm := NewSessionManager()
		
		// Get session (should create new one)
		session := sm.GetSession("datasource-1", 123)
		assert.NotNil(t, session)
		assert.Equal(t, int64(123), session.DatasourceID)
		assert.False(t, session.Initialized)
		assert.Empty(t, session.ID)
		
		// Get same session again
		session2 := sm.GetSession("datasource-1", 123)
		assert.Equal(t, session, session2)
	})
	
	t.Run("set session ID", func(t *testing.T) {
		sm := NewSessionManager()
		
		// Create session
		session := sm.GetSession("datasource-1", 123)
		
		// Set session ID
		sm.SetSessionID("datasource-1", "session-abc")
		
		// Verify it was set
		assert.Equal(t, "session-abc", session.ID)
	})
	
	t.Run("set tools", func(t *testing.T) {
		sm := NewSessionManager()
		
		// Create session
		session := sm.GetSession("datasource-1", 123)
		
		// Set tools
		tools := []mcp.Tool{
			{Name: "tool1", Description: "Tool 1"},
			{Name: "tool2", Description: "Tool 2"},
		}
		sm.SetTools("datasource-1", tools)
		
		// Verify
		assert.True(t, session.Initialized)
		assert.Len(t, session.Tools, 2)
		assert.Equal(t, "tool1", session.Tools[0].Name)
	})
	
	t.Run("cleanup stale sessions", func(t *testing.T) {
		sm := NewSessionManager()
		
		// Create two sessions
		session1 := sm.GetSession("datasource-1", 123)
		_ = sm.GetSession("datasource-2", 456) // Create session2 but don't need to use it
		
		// Make session1 stale
		session1.LastUsed = time.Now().Add(-2 * time.Hour)
		
		// Cleanup sessions older than 1 hour
		sm.CleanupStaleSessions(time.Hour)
		
		// Verify session1 is gone, session2 remains
		sm.mu.RLock()
		_, exists1 := sm.sessions["datasource-1"]
		_, exists2 := sm.sessions["datasource-2"]
		sm.mu.RUnlock()
		
		assert.False(t, exists1)
		assert.True(t, exists2)
	})
}

func TestJSONRPCRequestIDCounter(t *testing.T) {
	t.Run("sequential IDs", func(t *testing.T) {
		// Reset counter for test
		jsonrpcRequestIDCounter = 0
		
		id1 := getNextRequestID()
		id2 := getNextRequestID()
		id3 := getNextRequestID()
		
		assert.Equal(t, int64(1), id1)
		assert.Equal(t, int64(2), id2)
		assert.Equal(t, int64(3), id3)
	})
	
	t.Run("concurrent requests", func(t *testing.T) {
		// Reset counter for test
		jsonrpcRequestIDCounter = 0
		
		// Make concurrent requests
		const numRequests = 100
		ids := make(chan int64, numRequests)
		
		for i := 0; i < numRequests; i++ {
			go func() {
				ids <- getNextRequestID()
			}()
		}
		
		// Collect all IDs
		uniqueIDs := make(map[int64]bool)
		for i := 0; i < numRequests; i++ {
			id := <-ids
			uniqueIDs[id] = true
		}
		
		// Verify all IDs are unique
		assert.Len(t, uniqueIDs, numRequests)
	})
} 

func TestGetDatasource(t *testing.T) {
	// TODO: Add tests for getDatasource function
} 
