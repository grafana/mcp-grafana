//go:build unit
// +build unit

package mcpgrafana

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDynamicToolManager(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	dtm := NewDynamicToolManager(srv)

	assert.NotNil(t, dtm)
	assert.NotNil(t, dtm.server)
	assert.NotNil(t, dtm.toolsets)
	assert.NotNil(t, dtm.enabled)
}

func TestRegisterAndListToolsets(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	dtm := NewDynamicToolManager(srv)

	// Create a test toolset
	toolset := &Toolset{
		Name:        "test_toolset",
		Description: "A test toolset for unit testing",
		Tools:       []Tool{},
		AddFunc: func(s *server.MCPServer) {
			// Mock add function
		},
	}

	// Register the toolset
	dtm.RegisterToolset(toolset)

	// List toolsets
	toolsets := dtm.ListToolsets()
	require.Len(t, toolsets, 1)
	assert.Equal(t, "test_toolset", toolsets[0].Name)
	assert.Equal(t, "A test toolset for unit testing", toolsets[0].Description)
	assert.False(t, toolsets[0].Enabled) // Should be disabled by default
}

func TestEnableToolset(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	dtm := NewDynamicToolManager(srv)

	// Track if AddFunc was called
	addFuncCalled := false

	// Create a test toolset
	toolset := &Toolset{
		Name:        "test_toolset",
		Description: "A test toolset",
		Tools:       []Tool{},
		AddFunc: func(s *server.MCPServer) {
			addFuncCalled = true
		},
	}

	dtm.RegisterToolset(toolset)

	// Enable the toolset
	ctx := context.Background()
	err := dtm.EnableToolset(ctx, "test_toolset")
	require.NoError(t, err)
	assert.True(t, addFuncCalled, "AddFunc should have been called")

	// Check that it's enabled
	toolsets := dtm.ListToolsets()
	require.Len(t, toolsets, 1)
	assert.True(t, toolsets[0].Enabled)

	// Enabling again should not error
	err = dtm.EnableToolset(ctx, "test_toolset")
	require.NoError(t, err)
}

func TestEnableNonExistentToolset(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	dtm := NewDynamicToolManager(srv)

	ctx := context.Background()
	err := dtm.EnableToolset(ctx, "non_existent_toolset")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "toolset not found")
}

func TestDisableToolset(t *testing.T) {
	srv := server.NewMCPServer("test-server", "1.0.0")
	dtm := NewDynamicToolManager(srv)

	// Create and register a toolset
	toolset := &Toolset{
		Name:        "test_toolset",
		Description: "A test toolset",
		Tools:       []Tool{},
		AddFunc:     nil,
	}
	dtm.RegisterToolset(toolset)

	// Enable it first
	ctx := context.Background()
	err := dtm.EnableToolset(ctx, "test_toolset")
	require.NoError(t, err)

	// Verify it's enabled
	toolsets := dtm.ListToolsets()
	require.Len(t, toolsets, 1)
	assert.True(t, toolsets[0].Enabled)

	// Disable it
	err = dtm.DisableToolset("test_toolset")
	require.NoError(t, err)

	// Verify it's disabled
	toolsets = dtm.ListToolsets()
	require.Len(t, toolsets, 1)
	assert.False(t, toolsets[0].Enabled)

	// Try to disable a non-existent toolset
	err = dtm.DisableToolset("non_existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "toolset not found")
}
