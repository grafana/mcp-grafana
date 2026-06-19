//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	datasourceschemas "github.com/grafana/mcp-grafana/tools/datasource_schemas"
	"github.com/mark3labs/mcp-go/mcp"
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
	ctx := mcpgrafana.WithGrafanaClient(context.Background(), &mcpgrafana.GrafanaClient{GrafanaHTTPAPI: c})
	return mcpgrafana.WithGrafanaConfig(ctx, mcpgrafana.GrafanaConfig{URL: server.URL})
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

// ---- createDatasource ----
func TestCreateDatasource_NoSchemaGuidancePhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Grafana API must not be called during no-schema guidance phase")
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)

	// No schema for this type and no name yet — should return field guidance, not create.
	result, err := createDatasource(ctx, CreateDatasourceParams{
		Type: "nonexistent-plugin",
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var guidance map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &guidance))
	assert.Equal(t, "nonexistent-plugin", guidance["type"])
	// Relevant arguments live in the tool's input schema, not the guidance body;
	// the message must steer the caller to top-level args, not the fields map.
	message, _ := guidance["message"].(string)
	assert.Contains(t, message, "top-level arguments")
	assert.Contains(t, message, "fields map")
}

func TestCreateDatasource_SchemaGuidancePhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Grafana API must not be called during schema guidance phase")
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)

	result, err := createDatasource(ctx, CreateDatasourceParams{
		Name: "My Prometheus",
		Type: "prometheus",
		// no Fields → Phase 1: return schema guidance
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var guidance map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &guidance))
	assert.Equal(t, "prometheus", guidance["type"])
	assert.NotEmpty(t, guidance["fields"])
	assert.NotEmpty(t, guidance["message"])
}

func TestCreateDatasource_EmptyFieldsMapStaysInGuidancePhase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Grafana API must not be called when an empty fields map is sent on the first call")
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)

	// "fields": {} unmarshals to a non-nil empty map. Without schemaReviewed it
	// must still be treated as the first phase and return guidance, not create.
	result, err := createDatasource(ctx, CreateDatasourceParams{
		Name:   "My Prometheus",
		Type:   "prometheus",
		Fields: map[string]any{},
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var guidance map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &guidance))
	assert.Equal(t, "prometheus", guidance["type"])
	assert.NotEmpty(t, guidance["fields"])
}

func TestCreateDatasource_SchemaReviewedCreatesWithoutFields(t *testing.T) {
	id := int64(9)
	name := "My CloudWatch"
	uid := "cw-uid"
	msg := "Datasource added"
	mockResp := models.AddDataSourceOKBody{
		ID:         &id,
		Name:       &name,
		Message:    &msg,
		Datasource: &models.DataSource{ID: id, UID: uid, Name: name, Type: "cloudwatch"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/datasources":
			_ = json.NewEncoder(w).Encode(mockResp)
		case r.Method == http.MethodGet && r.URL.Path == "/api/datasources/uid/"+uid+"/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Data source connected"})
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)
	mcpgrafana.GrafanaClientFromContext(ctx).PublicURL = "https://grafana.example.com"

	// cloudwatch has no required fields; schemaReviewed=true alone must let it
	// create rather than loop in the guidance phase.
	result, err := createDatasource(ctx, CreateDatasourceParams{
		Name:           name,
		Type:           "cloudwatch",
		SchemaReviewed: true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
}

func TestCreateDatasource_SchemaReviewedWithoutNameReturnsGuidance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Grafana API must not be called when schemaReviewed is true but name is missing")
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)

	result, err := createDatasource(ctx, CreateDatasourceParams{
		Type:           "prometheus",
		SchemaReviewed: true,
	})
	require.NoError(t, err)
	require.Len(t, result.Content, 1)

	text, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var guidance map[string]any
	require.NoError(t, json.Unmarshal([]byte(text.Text), &guidance))
	assert.Equal(t, "prometheus", guidance["type"])
	message, _ := guidance["message"].(string)
	assert.Contains(t, message, "top-level `name` argument")
}

func TestCreateDatasource_NoSchemaCreatesDirectly(t *testing.T) {
	id := int64(7)
	name := "My Custom DS"
	uid := "custom-uid"
	msg := "Datasource added"
	mockResp := models.AddDataSourceOKBody{
		ID:         &id,
		Name:       &name,
		Message:    &msg,
		Datasource: &models.DataSource{ID: id, UID: uid, Name: name, Type: "nonexistent-plugin"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/datasources":
			_ = json.NewEncoder(w).Encode(mockResp)
		case r.Method == http.MethodGet && r.URL.Path == "/api/datasources/uid/"+uid+"/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Data source connected"})
		default:
			assert.Failf(t, "unexpected request", "%s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)
	mcpgrafana.GrafanaClientFromContext(ctx).PublicURL = "https://grafana.example.com"

	// No schema for this type — should create immediately without a fields step.
	result, err := createDatasource(ctx, CreateDatasourceParams{
		Name: name,
		Type: "nonexistent-plugin",
		URL:  "http://custom:9090",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
}

func TestCreateDatasource_Success(t *testing.T) {
	id := int64(42)
	name := "My Prometheus"
	msg := "Datasource added"
	uid := "new-prom-uid"

	mockResp := models.AddDataSourceOKBody{
		ID:      &id,
		Name:    &name,
		Message: &msg,
		Datasource: &models.DataSource{
			ID:   id,
			UID:  uid,
			Name: name,
			Type: "prometheus",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/datasources":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(mockResp)
		case r.Method == http.MethodGet && r.URL.Path == "/api/datasources/uid/"+uid+"/health":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Data source connected"})
		}
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)
	mcpgrafana.GrafanaClientFromContext(ctx).PublicURL = "https://grafana.example.com/"

	toolResult, err := createDatasource(ctx, CreateDatasourceParams{
		Name:           name,
		Type:           "prometheus",
		URL:            "http://prometheus:9090",
		Fields:         map[string]any{"httpMethod": "POST", "timeInterval": "15s"},
		SchemaReviewed: true,
	})
	require.NoError(t, err)
	require.NotNil(t, toolResult)
	assert.False(t, toolResult.IsError)
	require.Len(t, toolResult.Content, 2)

	text, ok := toolResult.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var got CreateDatasourceResult
	require.NoError(t, json.Unmarshal([]byte(text.Text), &got))
	assert.Equal(t, uid, got.UID)
	assert.Equal(t, name, got.Name)
	assert.Equal(t, msg, got.Message)
	assert.Equal(t, id, got.ID)
	require.NotNil(t, got.Health)
	assert.Equal(t, uid, got.Health.UID)
	assert.Equal(t, "Data source connected", got.Health.Message)

	configPageURL := "https://grafana.example.com/connections/datasources/edit/" + uid
	assert.Contains(t, got.NextSteps, configPageURL)

	link, ok := toolResult.Content[1].(mcp.ResourceLink)
	require.True(t, ok)
	assert.Equal(t, "resource_link", link.Type)
	assert.Equal(t, configPageURL, link.URI)
	assert.Equal(t, name, link.Name)
}

// --- updateDatasource ---

func newUpdateDatasourceServer(t *testing.T, current *models.DataSource, captureBody *models.UpdateDataSourceCommand, healthMsg string, healthGrafanaStatus string) *httptest.Server {
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
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": healthGrafanaStatus, "message": healthMsg})
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
	srv := newUpdateDatasourceServer(t, current, &captured, "Health check passed", "OK")
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
	srv := newUpdateDatasourceServer(t, current, nil, "Data source is working", "OK")
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
	srv := newUpdateDatasourceServer(t, current, nil, "connection refused", "ERROR")
	defer srv.Close()

	newURL := "http://bad-host:9090"
	result, err := updateDatasource(mockDatasourcesCtx(srv), UpdateDatasourceParams{UID: "prom-1", URL: &newURL})
	require.NoError(t, err)
	require.NotNil(t, result.Health)
	assert.Equal(t, "ERROR", result.Health.Status)
	assert.Equal(t, "connection refused", result.Health.Message)
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

func TestUpdateDatasource_PreservesPlainTextAuthFields(t *testing.T) {
	// User and BasicAuthUser are plain-text fields returned by Grafana and
	// must be preserved in the full update command.
	current := &models.DataSource{
		ID:            1,
		UID:           "prom-1",
		Name:          "Prometheus",
		Type:          "prometheus",
		User:          "db-user",
		BasicAuthUser: "ba-user",
	}
	var captured models.UpdateDataSourceCommand
	srv := newUpdateDatasourceServer(t, current, &captured, "OK", "OK")
	defer srv.Close()

	newURL := "http://prometheus:9090"
	_, err := updateDatasource(mockDatasourcesCtx(srv), UpdateDatasourceParams{UID: "prom-1", URL: &newURL})
	require.NoError(t, err)

	assert.Equal(t, "db-user", captured.User)
	assert.Equal(t, "ba-user", captured.BasicAuthUser)
	assert.Nil(t, captured.SecureJSONData, "SecureJSONData must not be forwarded in update command")
}

// --- checkDatasourceHealth ---

func TestCheckDatasourceHealth_ReturnsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/uid/prom-1/health", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "OK", "message": "Data source connected"})
	}))
	defer srv.Close()

	result, err := checkDatasourceHealth(mockDatasourcesCtx(srv), CheckDatasourceHealthParams{UID: "prom-1"})
	require.NoError(t, err)
	assert.Equal(t, "prom-1", result.UID)
	assert.Equal(t, "OK", result.Status)
	assert.Equal(t, "Data source connected", result.Message)
}

func TestCheckDatasourceHealth_HTTP200WithErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/uid/prom-1/health", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ERROR", "message": "connection refused"})
	}))
	defer srv.Close()

	result, err := checkDatasourceHealth(mockDatasourcesCtx(srv), CheckDatasourceHealthParams{UID: "prom-1"})
	require.NoError(t, err)
	assert.Equal(t, "prom-1", result.UID)
	assert.Equal(t, "ERROR", result.Status)
	assert.Equal(t, "connection refused", result.Message)
}

func TestCheckDatasourceHealth_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"data source not found"}`))
	}))
	defer srv.Close()

	_, err := checkDatasourceHealth(mockDatasourcesCtx(srv), CheckDatasourceHealthParams{UID: "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

// --- checkDatasourcesHealth ---

func mockDatasourceList(uids []string, dsType string) []*models.DataSourceListItemDTO {
	list := make([]*models.DataSourceListItemDTO, len(uids))
	for i, uid := range uids {
		list[i] = &models.DataSourceListItemDTO{UID: uid, Name: "DS " + uid, Type: dsType}
	}
	return list
}

func makeUIDList(n int) []string {
	uids := make([]string, n)
	for i := range uids {
		uids[i] = fmt.Sprintf("ds-%02d", i+1)
	}
	return uids
}

func allHealthy(uids []string) map[string]bool {
	m := make(map[string]bool, len(uids))
	for _, uid := range uids {
		m[uid] = true
	}
	return m
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
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "OK", "message": "Data source is working"})
		} else {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ERROR", "message": "connection refused"})
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
	assert.Equal(t, 3, result.Checked)
	assert.Equal(t, 3, result.Healthy)
	assert.Equal(t, 0, result.Unhealthy)
	assert.False(t, result.HasMore)
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
	assert.Equal(t, 2, result.Checked)
	assert.Equal(t, 2, result.Healthy)
	assert.False(t, result.HasMore)
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
	assert.Equal(t, 2, result.Checked)
	assert.False(t, result.HasMore)

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
	assert.Equal(t, 4, result.Checked)
	assert.Equal(t, 2, result.Healthy)
	assert.Equal(t, 2, result.Unhealthy)
	assert.False(t, result.HasMore)

	unhealthyCount := 0
	for _, r := range result.Results {
		if r.Status != "OK" {
			unhealthyCount++
		}
	}
	assert.Equal(t, 2, unhealthyCount)
}

func TestCheckDatasourcesHealth_UnknownUIDsProduceEmptyResult(t *testing.T) {
	list := mockDatasourceList([]string{"ds-1"}, "prometheus")
	srv := newBulkHealthServer(t, list, map[string]bool{"ds-1": true})
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{UIDs: []string{"does-not-exist"}})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Total)
	assert.Equal(t, 0, result.Checked)
	assert.Equal(t, 0, result.Healthy)
	assert.Equal(t, 0, result.Unhealthy)
	assert.False(t, result.HasMore)
}

func TestCheckDatasourcesHealth_Pagination_FirstPage(t *testing.T) {
	uids := makeUIDList(15)
	list := mockDatasourceList(uids, "prometheus")
	srv := newBulkHealthServer(t, list, allHealthy(uids))
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{})
	require.NoError(t, err)
	assert.Equal(t, 15, result.Total)
	assert.Equal(t, 10, result.Checked)
	assert.Equal(t, 10, result.Healthy)
	assert.Equal(t, 0, result.Unhealthy)
	assert.True(t, result.HasMore)
	assert.Len(t, result.Results, 10)
}

func TestCheckDatasourcesHealth_Pagination_SecondPage(t *testing.T) {
	uids := makeUIDList(15)
	list := mockDatasourceList(uids, "prometheus")
	srv := newBulkHealthServer(t, list, allHealthy(uids))
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{Offset: 10})
	require.NoError(t, err)
	assert.Equal(t, 15, result.Total)
	assert.Equal(t, 5, result.Checked)
	assert.Equal(t, 5, result.Healthy)
	assert.Equal(t, 0, result.Unhealthy)
	assert.False(t, result.HasMore)
	assert.Len(t, result.Results, 5)
}

func TestCheckDatasourcesHealth_Pagination_OffsetBeyondTotal(t *testing.T) {
	list := mockDatasourceList([]string{"ds-1", "ds-2"}, "prometheus")
	srv := newBulkHealthServer(t, list, map[string]bool{"ds-1": true, "ds-2": true})
	defer srv.Close()

	result, err := checkDatasourcesHealth(mockDatasourcesCtx(srv), BulkCheckDatasourceHealthParams{Offset: 20})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, 0, result.Checked)
	assert.Equal(t, 0, result.Healthy)
	assert.Equal(t, 0, result.Unhealthy)
	assert.False(t, result.HasMore)
	assert.Empty(t, result.Results)
}

func TestCreateDatasource_SecureFieldsNotLeakedToJSONData(t *testing.T) {
	var capturedJSONData map[string]any

	id := int64(1)
	name := "My Prometheus"
	uid := "prom-uid"
	msg := "ok"
	mockResp := models.AddDataSourceOKBody{
		ID:         &id,
		Name:       &name,
		Message:    &msg,
		Datasource: &models.DataSource{ID: id, UID: uid, Name: name, Type: "prometheus"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/datasources":
			var body struct {
				JSONData map[string]any `json:"jsonData"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturedJSONData = body.JSONData
			_ = json.NewEncoder(w).Encode(mockResp)
		case r.Method == http.MethodGet && r.URL.Path == "/api/datasources/uid/"+uid+"/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Data source connected"})
		}
	}))
	defer srv.Close()

	_, err := createDatasource(mockDatasourcesCtx(srv), CreateDatasourceParams{
		Name: name,
		Type: "prometheus",
		Fields: map[string]any{
			"httpMethod":        "GET",
			"basicAuthPassword": "s3cr3t", // secureJsonData field — must be dropped
		},
		SchemaReviewed: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "GET", capturedJSONData["httpMethod"])
	assert.NotContains(t, capturedJSONData, "basicAuthPassword")
}

// ---- applyFields ----

func TestApplyFields(t *testing.T) {
	schema := &datasourceschemas.DatasourceSchema{
		Fields: []datasourceschemas.DsSchemaField{
			{Key: "httpMethod", Target: "jsonData", ValueType: "string"},
			{Key: "timeInterval", Target: "jsonData", ValueType: "string"},
			{Key: "basicAuthPassword", Target: "secureJsonData", ValueType: "string"},
			{Key: "url", Target: "root", ValueType: "string"},
			{ID: "root.user", Key: "user", Target: "root", ValueType: "string"},
			{Key: "basicAuth", Target: "root", ValueType: "boolean"},
			{Key: "isDefault", Target: "root", ValueType: "boolean"},
		},
	}

	t.Run("jsonData fields are returned in jsonData map", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, schema, map[string]any{"httpMethod": "POST"})
		assert.Equal(t, "POST", result["httpMethod"])
	})

	t.Run("secureJsonData fields are excluded", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, schema, map[string]any{"basicAuthPassword": "s3cr3t"})
		assert.NotContains(t, result, "basicAuthPassword")
	})

	t.Run("root url is applied to body and not in jsonData", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, schema, map[string]any{"url": "http://prometheus:9090"})
		assert.Equal(t, "http://prometheus:9090", body.URL)
		assert.NotContains(t, result, "url")
	})

	t.Run("root basicAuth is applied to body", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		applyFields(body, schema, map[string]any{"basicAuth": true})
		assert.True(t, body.BasicAuth)
	})

	t.Run("root isDefault is applied to body", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		applyFields(body, schema, map[string]any{"isDefault": true})
		assert.True(t, body.IsDefault)
	})

	t.Run("excluded root user is neither applied nor placed in jsonData", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, schema, map[string]any{"user": "postgres"})
		assert.Empty(t, body.User)
		assert.NotContains(t, result, "user")
	})

	t.Run("excluded jsonData user is not placed in jsonData", func(t *testing.T) {
		s := &datasourceschemas.DatasourceSchema{
			Fields: []datasourceschemas.DsSchemaField{
				{ID: "jsonData.user", Key: "user", Target: "jsonData", ValueType: "string"},
			},
		}
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, s, map[string]any{"user": "service-account"})
		assert.NotContains(t, result, "user")
	})

	t.Run("unknown fields are excluded", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, schema, map[string]any{"unknownField": "value"})
		assert.NotContains(t, result, "unknownField")
	})

	t.Run("section-prefixed input key is stored under nested section", func(t *testing.T) {
		s := &datasourceschemas.DatasourceSchema{
			Fields: []datasourceschemas.DsSchemaField{
				{Key: "region", Section: "aws", Target: "jsonData", ValueType: "string"},
			},
		}
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, s, map[string]any{"aws.region": "us-east-1"})
		assert.Equal(t, map[string]any{"region": "us-east-1"}, result["aws"])
		assert.NotContains(t, result, "region")
		assert.NotContains(t, result, "aws.region")
	})

	t.Run("section-prefixed input keys do not collide", func(t *testing.T) {
		s := &datasourceschemas.DatasourceSchema{
			Fields: []datasourceschemas.DsSchemaField{
				{Key: "defaultDatabase", Target: "jsonData", ValueType: "string"},
				{Key: "defaultDatabase", Section: "logs", Target: "jsonData", ValueType: "string"},
				{Key: "defaultDatabase", Section: "traces", Target: "jsonData", ValueType: "string"},
			},
		}
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, s, map[string]any{
			"defaultDatabase":        "queries",
			"logs.defaultDatabase":   "logs",
			"traces.defaultDatabase": "traces",
		})
		assert.Equal(t, "queries", result["defaultDatabase"])
		assert.Equal(t, map[string]any{"defaultDatabase": "logs"}, result["logs"])
		assert.Equal(t, map[string]any{"defaultDatabase": "traces"}, result["traces"])
	})

	t.Run("common root fields are applied to body", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		applyFields(body, &datasourceschemas.DatasourceSchema{}, map[string]any{
			"uid":       "custom-uid",
			"isDefault": true,
		})
		assert.Equal(t, "custom-uid", body.UID)
		assert.True(t, body.IsDefault)
	})

	t.Run("mixed targets: root to body, jsonData to map, secrets excluded", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, schema, map[string]any{
			"url":               "http://prometheus:9090",
			"httpMethod":        "GET",
			"timeInterval":      "30s",
			"basicAuthPassword": "s3cr3t",
			"extra":             "fallback",
		})
		assert.Equal(t, "http://prometheus:9090", body.URL)
		assert.Equal(t, "GET", result["httpMethod"])
		assert.Equal(t, "30s", result["timeInterval"])
		assert.NotContains(t, result, "url")
		assert.NotContains(t, result, "basicAuthPassword")
		assert.NotContains(t, result, "extra")
	})
}
