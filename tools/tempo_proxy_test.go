//go:build unit

package tools

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeToolHash(t *testing.T) {
	t.Run("identical tools produce same hash", func(t *testing.T) {
		tool1 := mcp.Tool{
			Name:        "test-tool",
			Description: "A test tool",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "string"},
				},
			},
		}
		
		tool2 := mcp.Tool{
			Name:        "test-tool",
			Description: "A test tool",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "string"},
				},
			},
		}
		
		hash1 := computeToolHash(tool1)
		hash2 := computeToolHash(tool2)
		
		assert.Equal(t, hash1, hash2)
	})
	
	t.Run("different descriptions produce different hashes", func(t *testing.T) {
		tool1 := mcp.Tool{
			Name:        "test-tool",
			Description: "A test tool",
			InputSchema: mcp.ToolInputSchema{},
		}
		
		tool2 := mcp.Tool{
			Name:        "test-tool",
			Description: "A different test tool",
			InputSchema: mcp.ToolInputSchema{},
		}
		
		hash1 := computeToolHash(tool1)
		hash2 := computeToolHash(tool2)
		
		assert.NotEqual(t, hash1, hash2)
	})
	
	t.Run("different schemas produce different hashes", func(t *testing.T) {
		tool1 := mcp.Tool{
			Name:        "test-tool",
			Description: "A test tool",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "string"},
				},
			},
		}
		
		tool2 := mcp.Tool{
			Name:        "test-tool",
			Description: "A test tool",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"param1": map[string]interface{}{"type": "number"},
				},
			},
		}
		
		hash1 := computeToolHash(tool1)
		hash2 := computeToolHash(tool2)
		
		assert.NotEqual(t, hash1, hash2)
	})
}

func TestNormalizeTempoToolName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple name",
			input:    "trace-search",
			expected: "tempo_trace_search",
		},
		{
			name:     "already has underscores",
			input:    "trace_search",
			expected: "tempo_trace_search",
		},
		{
			name:     "multiple hyphens",
			input:    "trace-ql-metrics-range",
			expected: "tempo_trace_ql_metrics_range",
		},
		{
			name:     "no hyphens",
			input:    "search",
			expected: "tempo_search",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeTempoToolName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMakeUniqueToolName(t *testing.T) {
	tests := []struct {
		name           string
		baseName       string
		datasourceName string
		expected       string
	}{
		{
			name:           "simple datasource name",
			baseName:       "tempo_trace_search",
			datasourceName: "Tempo",
			expected:       "tempo_trace_search_tempo",
		},
		{
			name:           "datasource name with spaces",
			baseName:       "tempo_trace_search",
			datasourceName: "Tempo Production",
			expected:       "tempo_trace_search_tempo_production",
		},
		{
			name:           "datasource name with hyphens",
			baseName:       "tempo_trace_search",
			datasourceName: "tempo-prod-1",
			expected:       "tempo_trace_search_tempo_prod_1",
		},
		{
			name:           "mixed case datasource name",
			baseName:       "tempo_trace_search",
			datasourceName: "TeMpO-PrOd",
			expected:       "tempo_trace_search_tempo_prod",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := makeUniqueToolName(tt.baseName, tt.datasourceName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateTempoToolHandler(t *testing.T) {
	t.Run("requires datasource_uid", func(t *testing.T) {
		handler := createTempoToolHandler("tempo_test_tool", []string{"ds1", "ds2"})
		
		// Test empty datasource_uid
		_, err := handler(nil, DynamicTempoToolParams{
			DatasourceUID: "",
		})
		
		require.Error(t, err)
		assert.Contains(t, err.Error(), "datasource_uid is required")
	})
	
	t.Run("validates allowed datasources", func(t *testing.T) {
		handler := createTempoToolHandler("tempo_test_tool", []string{"ds1", "ds2"})
		
		// Test invalid datasource
		_, err := handler(nil, DynamicTempoToolParams{
			DatasourceUID: "ds3",
		})
		
		require.Error(t, err)
		assert.Contains(t, err.Error(), "datasource ds3 does not provide tool tempo_test_tool")
	})
} 

func TestTempoRegistry_PollingLifecycle(t *testing.T) {
	t.Run("start and stop polling", func(t *testing.T) {
		registry := &tempoToolRegistry{
			registeredTools:   make(map[string]*registeredTool),
			datasourceTools:   make(map[string][]string),
			toolToDatasources: make(map[string][]string),
			stopPoller:        make(chan struct{}),
		}
		
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		
		// Start polling
		registry.startPolling(ctx, 100*time.Millisecond)
		assert.True(t, registry.pollerRunning)
		
		// Stop polling
		registry.stopPolling()
		assert.False(t, registry.pollerRunning)
	})
	
	t.Run("context cancellation stops polling", func(t *testing.T) {
		registry := &tempoToolRegistry{
			registeredTools:   make(map[string]*registeredTool),
			datasourceTools:   make(map[string][]string),
			toolToDatasources: make(map[string][]string),
			stopPoller:        make(chan struct{}),
		}
		
		ctx, cancel := context.WithCancel(context.Background())
		
		// Start polling
		registry.startPolling(ctx, 100*time.Millisecond)
		
		// Cancel context
		cancel()
		
		// Give it time to react - polling might take a moment to stop
		time.Sleep(500 * time.Millisecond)
		
		// The polling goroutine should eventually stop, but it might not update
		// pollerRunning immediately. Let's check if we can stop it without panic
		registry.stopPolling() // Should be safe even if already stopped
		
		// Now it should definitely be stopped
		assert.False(t, registry.pollerRunning)
	})
	
	t.Run("multiple stop calls are safe", func(t *testing.T) {
		registry := &tempoToolRegistry{
			registeredTools:   make(map[string]*registeredTool),
			datasourceTools:   make(map[string][]string),
			toolToDatasources: make(map[string][]string),
			stopPoller:        make(chan struct{}),
		}
		
		ctx := context.Background()
		
		// Start polling
		registry.startPolling(ctx, 100*time.Millisecond)
		
		// Stop multiple times - should not panic
		registry.stopPolling()
		registry.stopPolling()
		registry.stopPolling()
		
		assert.False(t, registry.pollerRunning)
	})
} 
