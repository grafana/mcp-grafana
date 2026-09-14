//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sloclient "github.com/grafana/gcx/client/slo"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSLOTestContext(t *testing.T, slos []sloclient.Slo) context.Context {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"slos": slos})
	}))
	t.Cleanup(srv.Close)

	client := sloclient.NewClient(srv.Client(), srv.URL)
	return mcpgrafana.WithSLOClient(context.Background(), client)
}

func TestSLOTools(t *testing.T) {
	t.Run("list slos", func(t *testing.T) {
		ctx := newSLOTestContext(t, []sloclient.Slo{
			{
				UUID:        "slo-1",
				Name:        "HTTP Availability",
				Description: "Tracks HTTP request success rate",
				Objectives:  []sloclient.Objective{{Value: 0.995, Window: "28d"}},
				ReadOnly:    &sloclient.ReadOnly{Status: &sloclient.Status{Type: "ok"}},
			},
			{UUID: "slo-2", Name: "Latency"},
		})

		result, err := listSLOs(ctx, ListSLOsParams{})
		require.NoError(t, err)
		require.Len(t, result.SLOs, 2)
		assert.Equal(t, "slo-1", result.SLOs[0].UUID)
		assert.Equal(t, []string{"99.500% over 28d"}, result.SLOs[0].Objectives)
		assert.Equal(t, "ok", result.SLOs[0].Status)
		assert.False(t, result.HasMore)
	})

	t.Run("list slos respects limit", func(t *testing.T) {
		ctx := newSLOTestContext(t, []sloclient.Slo{
			{UUID: "slo-1", Name: "A"},
			{UUID: "slo-2", Name: "B"},
			{UUID: "slo-3", Name: "C"},
		})

		result, err := listSLOs(ctx, ListSLOsParams{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, result.SLOs, 2)
		assert.True(t, result.HasMore)
	})

	t.Run("no client configured", func(t *testing.T) {
		_, err := listSLOs(context.Background(), ListSLOsParams{})
		require.Error(t, err)
	})
}
