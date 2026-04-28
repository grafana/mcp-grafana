//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockDatasourcesCtx(server *httptest.Server) context.Context {
	u, _ := url.Parse(server.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	cfg.APIKey = "test"

	c := client.NewHTTPClientWithConfig(nil, cfg)
	return mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
}

func createMockDatasources(count int) []*models.DataSource {
	datasources := make([]*models.DataSource, count)
	for i := 0; i < count; i++ {
		datasources[i] = &models.DataSource{
			ID:        int64(i + 1),
			UID:       "ds-" + string(rune('a'+i)),
			Name:      "Datasource " + string(rune('A'+i)),
			Type:      "prometheus",
			IsDefault: i == 0,
		}
	}
	return datasources
}

func TestListDatasources_Pagination(t *testing.T) {
	// Create 10 mock datasources
	mockDS := createMockDatasources(10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockDS)
	}))
	defer server.Close()

	ctx := mockDatasourcesCtx(server)

	t.Run("default pagination returns first 50 (all 10)", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{})
		require.NoError(t, err)
		assert.Len(t, result.Datasources, 10)
		assert.Equal(t, 10, result.Total)
		assert.False(t, result.HasMore)
	})

	t.Run("limit restricts results", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{Limit: 3})
		require.NoError(t, err)
		assert.Len(t, result.Datasources, 3)
		assert.Equal(t, 10, result.Total)
		assert.True(t, result.HasMore)
	})

	t.Run("offset skips results", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{Limit: 3, Offset: 2})
		require.NoError(t, err)
		assert.Len(t, result.Datasources, 3)
		assert.Equal(t, 10, result.Total)
		assert.True(t, result.HasMore)
		assert.Equal(t, "ds-c", result.Datasources[0].UID)
	})

	t.Run("offset beyond total returns empty", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{Offset: 20})
		require.NoError(t, err)
		assert.Len(t, result.Datasources, 0)
		assert.Equal(t, 10, result.Total)
		assert.False(t, result.HasMore)
	})

	t.Run("limit capped at 100", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{Limit: 200})
		require.NoError(t, err)
		// Since we only have 10 datasources, we get all 10
		assert.Len(t, result.Datasources, 10)
		assert.Equal(t, 10, result.Total)
		assert.False(t, result.HasMore)
	})

	t.Run("last page has hasMore=false", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{Limit: 3, Offset: 9})
		require.NoError(t, err)
		assert.Len(t, result.Datasources, 1)
		assert.Equal(t, 10, result.Total)
		assert.False(t, result.HasMore)
	})
}

func TestListDatasources_TypeFilter(t *testing.T) {
	// Create mixed type datasources
	mockDS := []*models.DataSource{
		{ID: 1, UID: "prom-1", Name: "Prometheus 1", Type: "prometheus"},
		{ID: 2, UID: "loki-1", Name: "Loki 1", Type: "loki"},
		{ID: 3, UID: "prom-2", Name: "Prometheus 2", Type: "prometheus"},
		{ID: 4, UID: "tempo-1", Name: "Tempo 1", Type: "tempo"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockDS)
	}))
	defer server.Close()

	ctx := mockDatasourcesCtx(server)

	t.Run("filter by type with pagination", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{Type: "prometheus", Limit: 1})
		require.NoError(t, err)
		assert.Len(t, result.Datasources, 1)
		assert.Equal(t, 2, result.Total) // 2 prometheus datasources total
		assert.True(t, result.HasMore)
		assert.Equal(t, "prom-1", result.Datasources[0].UID)
	})

	t.Run("filter by type second page", func(t *testing.T) {
		result, err := listDatasources(ctx, ListDatasourcesParams{Type: "prometheus", Limit: 1, Offset: 1})
		require.NoError(t, err)
		assert.Len(t, result.Datasources, 1)
		assert.Equal(t, 2, result.Total)
		assert.False(t, result.HasMore)
		assert.Equal(t, "prom-2", result.Datasources[0].UID)
	})
}

func TestGetDatasource_RoutesToUID(t *testing.T) {
	mockDS := &models.DataSource{
		ID:   1,
		UID:  "test-uid",
		Name: "Test DS",
		Type: "prometheus",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/uid/test-uid", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockDS)
	}))
	defer server.Close()

	ctx := mockDatasourcesCtx(server)

	result, err := getDatasource(ctx, GetDatasourceParams{UID: "test-uid"})
	require.NoError(t, err)
	assert.Equal(t, "Test DS", result.Name)
}

func TestGetDatasource_RoutesToName(t *testing.T) {
	mockDS := &models.DataSource{
		ID:   1,
		UID:  "test-uid",
		Name: "Test DS",
		Type: "prometheus",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/name/Test DS", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockDS)
	}))
	defer server.Close()

	ctx := mockDatasourcesCtx(server)

	result, err := getDatasource(ctx, GetDatasourceParams{Name: "Test DS"})
	require.NoError(t, err)
	assert.Equal(t, "test-uid", result.UID)
}

func TestGetDatasource_UIDTakesPriority(t *testing.T) {
	mockDS := &models.DataSource{
		ID:   1,
		UID:  "test-uid",
		Name: "Test DS",
		Type: "prometheus",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should use UID path, not name path
		assert.Equal(t, "/api/datasources/uid/test-uid", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockDS)
	}))
	defer server.Close()

	ctx := mockDatasourcesCtx(server)

	result, err := getDatasource(ctx, GetDatasourceParams{UID: "test-uid", Name: "Test DS"})
	require.NoError(t, err)
	assert.Equal(t, "Test DS", result.Name)
}

func TestGetDatasource_ErrorWhenNeitherProvided(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("should not make any HTTP request")
	}))
	defer server.Close()

	ctx := mockDatasourcesCtx(server)

	result, err := getDatasource(ctx, GetDatasourceParams{})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "either uid or name must be provided")
}

// --- updateDatasource ---

func newUpdateDatasourceServer(t *testing.T, current *models.DataSource, captureBody *models.UpdateDataSourceCommand, healthMsg string, healthStatus int) *httptest.Server {
	t.Helper()
	id := current.ID
	msg := "Datasource updated"
	name := current.Name
	updateResp := &models.UpdateDataSourceByUIDOKBody{
		Datasource: current,
		ID:         &id,
		Message:    &msg,
		Name:       &name,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/datasources/uid/"+current.UID:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(current)
		case r.Method == http.MethodPut && r.URL.Path == "/api/datasources/uid/"+current.UID:
			if captureBody != nil {
				_ = json.NewDecoder(r.Body).Decode(captureBody)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(updateResp)
		case r.Method == http.MethodGet && r.URL.Path == "/api/datasources/uid/"+current.UID+"/health":
			w.WriteHeader(healthStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": healthMsg})
		}
	}))
}

func TestUpdateDatasource_MergesProvidedFields(t *testing.T) {
	current := &models.DataSource{
		ID:   1,
		UID:  "prom-1",
		Name: "Old Name",
		Type: "prometheus",
		URL:  "http://old:9090",
	}
	var captured models.UpdateDataSourceCommand
	srv := newUpdateDatasourceServer(t, current, &captured, "Health check passed", http.StatusOK)
	defer srv.Close()

	newName := "New Name"
	_, err := updateDatasource(mockDatasourcesCtx(srv), UpdateDatasourceParams{UID: "prom-1", Name: &newName})
	require.NoError(t, err)

	assert.Equal(t, "New Name", captured.Name)
	assert.Equal(t, "http://old:9090", captured.URL) // unprovided field preserved from current
	assert.Equal(t, "prometheus", captured.Type)
}

func TestUpdateDatasource_HealthCheckIncludedInResult(t *testing.T) {
	current := &models.DataSource{ID: 1, UID: "prom-1", Name: "Prometheus", Type: "prometheus"}
	srv := newUpdateDatasourceServer(t, current, nil, "Data source is working", http.StatusOK)
	defer srv.Close()

	newURL := "http://new:9090"
	result, err := updateDatasource(mockDatasourcesCtx(srv), UpdateDatasourceParams{UID: "prom-1", URL: &newURL})
	require.NoError(t, err)
	require.NotNil(t, result.Health)
	assert.Equal(t, "prom-1", result.Health.UID)
	assert.Equal(t, "Data source is working", result.Health.Message)
}

func TestUpdateDatasource_HealthCheckFailureIsNonFatal(t *testing.T) {
	current := &models.DataSource{ID: 1, UID: "prom-1", Name: "Prometheus", Type: "prometheus"}
	srv := newUpdateDatasourceServer(t, current, nil, "connection refused", http.StatusInternalServerError)
	defer srv.Close()

	newURL := "http://bad-host:9090"
	result, err := updateDatasource(mockDatasourcesCtx(srv), UpdateDatasourceParams{UID: "prom-1", URL: &newURL})
	require.NoError(t, err) // update itself succeeded
	require.NotNil(t, result.Health)
	assert.Contains(t, result.Health.Message, "health check failed")
}

func TestUpdateDatasource_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()

	_, err := updateDatasource(mockDatasourcesCtx(srv), UpdateDatasourceParams{UID: "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- checkDatasourceHealth ---

func TestCheckDatasourceHealth_ReturnsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/uid/prom-1/health", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Data source connected"})
	}))
	defer srv.Close()

	result, err := checkDatasourceHealth(mockDatasourcesCtx(srv), CheckDatasourceHealthParams{UID: "prom-1"})
	require.NoError(t, err)
	assert.Equal(t, "prom-1", result.UID)
	assert.Equal(t, "Data source connected", result.Message)
}

func TestCheckDatasourceHealth_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"data source not found"}`))
	}))
	defer srv.Close()

	_, err := checkDatasourceHealth(mockDatasourcesCtx(srv), CheckDatasourceHealthParams{UID: "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- checkDatasourcesHealth ---

func mockDatasourceList(uids []string, dsType string) []*models.DataSourceListItemDTO {
	list := make([]*models.DataSourceListItemDTO, len(uids))
	for i, uid := range uids {
		list[i] = &models.DataSourceListItemDTO{UID: uid, Name: "DS " + uid, Type: dsType}
	}
	return list
}

func newBulkHealthServer(t *testing.T, list []*models.DataSourceListItemDTO, healthyUIDs map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/datasources" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		// /api/datasources/uid/{uid}/health
		parts := strings.Split(r.URL.Path, "/")
		uid := parts[len(parts)-2] // path: .../uid/{uid}/health
		if healthyUIDs[uid] {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "OK"})
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "connection error"})
		}
	}))
}

func TestCheckDatasourcesHealth_NoFilter_ChecksAll(t *testing.T) {
	list := mockDatasourceList([]string{"ds-1", "ds-2", "ds-3"}, "prometheus")
	srv := newBulkHealthServer(t, list, map[string]bool{"ds-1": true, "ds-2": true, "ds-3": true})
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{})
	require.NoError(t, err)
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, 3, result.Healthy)
	assert.Equal(t, 0, result.Unhealthy)
}

func TestCheckDatasourcesHealth_TypeFilter(t *testing.T) {
	list := []*models.DataSourceListItemDTO{
		{UID: "prom-1", Name: "Prometheus 1", Type: "prometheus"},
		{UID: "loki-1", Name: "Loki 1", Type: "loki"},
		{UID: "prom-2", Name: "Prometheus 2", Type: "prometheus"},
	}
	srv := newBulkHealthServer(t, list, map[string]bool{"prom-1": true, "prom-2": true})
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{Type: "prometheus"})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 2, result.Healthy)
	for _, r := range result.Results {
		assert.Equal(t, "prometheus", r.Type)
	}
}

func TestCheckDatasourcesHealth_ExplicitUIDs(t *testing.T) {
	list := mockDatasourceList([]string{"ds-1", "ds-2", "ds-3"}, "prometheus")
	srv := newBulkHealthServer(t, list, map[string]bool{"ds-1": true, "ds-2": true})
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{UIDs: []string{"ds-1", "ds-3"}})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Total)

	uids := make([]string, len(result.Results))
	for i, r := range result.Results {
		uids[i] = r.UID
	}
	assert.ElementsMatch(t, []string{"ds-1", "ds-3"}, uids)
}

func TestCheckDatasourcesHealth_HealthyCounts(t *testing.T) {
	list := mockDatasourceList([]string{"ds-1", "ds-2", "ds-3", "ds-4"}, "prometheus")
	srv := newBulkHealthServer(t, list, map[string]bool{"ds-1": true, "ds-3": true})
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{})
	require.NoError(t, err)
	assert.Equal(t, 4, result.Total)
	assert.Equal(t, 2, result.Healthy)
	assert.Equal(t, 2, result.Unhealthy)

	errCount := 0
	for _, r := range result.Results {
		if r.Error != "" {
			errCount++
		}
	}
	assert.Equal(t, 2, errCount)
}

func TestCheckDatasourcesHealth_UnknownUIDsProduceEmptyResult(t *testing.T) {
	list := mockDatasourceList([]string{"ds-1"}, "prometheus")
	srv := newBulkHealthServer(t, list, map[string]bool{"ds-1": true})
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{UIDs: []string{"does-not-exist"}})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Total)
	assert.Equal(t, 0, result.Healthy)
	assert.Equal(t, 0, result.Unhealthy)
}
