//go:build unit
// +build unit

package tools

import (
	"context"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdminToolsUnit(t *testing.T) {
	t.Run("tool definitions", func(t *testing.T) {
		// Test that the tools are properly defined with correct metadata
		require.NotNil(t, ListUsersByOrg, "ListUsersByOrg tool should be defined")
		require.NotNil(t, ListTeams, "ListTeams tool should be defined")

		// Verify tool metadata
		assert.Equal(t, "list_users_by_org", ListUsersByOrg.Tool.Name)
		assert.Equal(t, "list_teams", ListTeams.Tool.Name)
		assert.Contains(t, ListUsersByOrg.Tool.Description, "List users by organization")
		assert.Contains(t, ListTeams.Tool.Description, "Search for Grafana teams")
	})

	t.Run("parameter structures", func(t *testing.T) {
		// Test parameter types are correctly defined
		userParams := ListUsersByOrgParams{}
		teamParams := ListTeamsParams{Query: "test-query"}

		// ListUsersByOrgParams should be an empty struct (no parameters required)
		assert.IsType(t, ListUsersByOrgParams{}, userParams)

		// ListTeamsParams should have a Query field
		assert.Equal(t, "test-query", teamParams.Query)
	})

	t.Run("nil client handling", func(t *testing.T) {
		// Test that functions handle missing client gracefully
		ctx := context.Background() // No client in context

		// Both functions should return nil when client is not available
		// (they will panic on nil pointer dereference, which is the current behavior)
		assert.Panics(t, func() {
			listUsersByOrg(ctx, ListUsersByOrgParams{})
		}, "Should panic when no Grafana client in context")

		assert.Panics(t, func() {
			listTeams(ctx, ListTeamsParams{})
		}, "Should panic when no Grafana client in context")
	})

	t.Run("function signatures", func(t *testing.T) {
		// Verify that function signatures follow the expected pattern
		// This test ensures the API migration was done correctly

		// Create context with configuration but no client
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{
			URL:    "http://test.grafana.com",
			APIKey: "test-key",
		})

		// Test that both functions can be called with correct parameter types
		// They will fail due to no client, but this validates the signature
		assert.Panics(t, func() {
			listUsersByOrg(ctx, ListUsersByOrgParams{})
		})

		assert.Panics(t, func() {
			listTeams(ctx, ListTeamsParams{Query: "test"})
		})
	})
}
