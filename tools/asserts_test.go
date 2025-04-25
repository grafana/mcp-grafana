//go:build unit
// +build unit

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockAssertsServer(handler http.HandlerFunc) (*httptest.Server, context.Context) {
	server := httptest.NewServer(handler)
	ctx := context.Background()
	ctx = mcpgrafana.WithGrafanaURL(ctx, server.URL)
	ctx = mcpgrafana.WithGrafanaAPIKey(ctx, "test-api-key")
	return server, ctx
}

func TestAssertTools(t *testing.T) {
	t.Run("get assertions", func(t *testing.T) {
		server, ctx := setupMockAssertsServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/plugins/grafana-asserts-app/resources/asserts/api-server/v1/assertions/llm-summary", r.URL.Path)
			require.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(`{"summary": "test summary"}`))
			require.NoError(t, err)
		})
		defer server.Close()

		result, err := getAssertions(ctx, GetAssertionsParams{
			StartTime:  time.Date(2025, 4, 23, 10, 0, 0, 0, time.UTC),
			EndTime:    time.Date(2025, 4, 23, 11, 0, 0, 0, time.UTC),
			EntityType: "Service",
			EntityName: "mongodb",
			Env:        "asserts-demo",
			Site:       "app",
			Namespace:  "robot-shop",
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, `{"summary": "test summary"}`, result)
	})
}
