//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockSearchCtx(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	return mcpgrafana.WithGrafanaClient(context.Background(), c)
}

func TestSearchDashboards_Pagination(t *testing.T) {
	t.Run("default pagination uses limit 50 and page 1", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/search", r.URL.Path)
			q := r.URL.Query()
			assert.Equal(t, "50", q.Get("limit"))
			assert.Equal(t, "1", q.Get("page"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test"})
		require.NoError(t, err)
	})

	t.Run("custom limit and page are sent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "25", q.Get("limit"))
			assert.Equal(t, "3", q.Get("page"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 25, Page: 3})
		require.NoError(t, err)
	})

	t.Run("limit capped at 100", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "100", q.Get("limit"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 500})
		require.NoError(t, err)
	})

	t.Run("page defaults to 1 when 0 or negative", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "1", q.Get("page"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(models.HitList{})
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		_, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Page: 0})
		require.NoError(t, err)
	})

	t.Run("hasMore true when results equal limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return exactly 10 results (matching the limit)
			results := make(models.HitList, 10)
			for i := 0; i < 10; i++ {
				results[i] = &models.Hit{
					UID:   "dash-" + string(rune('a'+i)),
					Title: "Dashboard " + string(rune('A'+i)),
					Type:  "dash-db",
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(results)
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		result, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 10})
		require.NoError(t, err)
		assert.True(t, result.HasMore)
		assert.Len(t, result.Dashboards, 10)
	})

	t.Run("hasMore false when results less than limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return less than limit results
			results := make(models.HitList, 5)
			for i := 0; i < 5; i++ {
				results[i] = &models.Hit{
					UID:   "dash-" + string(rune('a'+i)),
					Title: "Dashboard " + string(rune('A'+i)),
					Type:  "dash-db",
				}
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(results)
		}))
		defer server.Close()

		ctx := mockSearchCtx(server)
		result, err := searchDashboards(ctx, SearchDashboardsParams{Query: "test", Limit: 10})
		require.NoError(t, err)
		assert.False(t, result.HasMore)
		assert.Len(t, result.Dashboards, 5)
	})
}
