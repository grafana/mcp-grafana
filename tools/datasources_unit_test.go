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
	assert.NotEmpty(t, guidance["message"])

	fields, ok := guidance["fields"].([]any)
	require.True(t, ok)
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		fm := f.(map[string]any)
		keys = append(keys, fm["key"].(string))
	}
	assert.Contains(t, keys, "name")
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

func TestCreateDatasource_NoSchemaCreatesDirectly(t *testing.T) {
	id := int64(7)
	name := "My Custom DS"
	uid := "custom-uid"
	msg := "Datasource added"
	mockResp := models.AddDataSourceOKBody{
		ID:      &id,
		Name:    &name,
		Message: &msg,
		Datasource: &models.DataSource{ID: id, UID: uid, Name: name, Type: "nonexistent-plugin"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResp)
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
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/datasources", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mockResp)
	}))
	defer srv.Close()

	ctx := mockDatasourcesCtx(srv)
	mcpgrafana.GrafanaClientFromContext(ctx).PublicURL = "https://grafana.example.com"

	toolResult, err := createDatasource(ctx, CreateDatasourceParams{
		Name:   name,
		Type:   "prometheus",
		URL:    "http://prometheus:9090",
		Fields: map[string]any{"httpMethod": "POST", "timeInterval": "15s"},
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

	configPageURL := "https://grafana.example.com/connections/datasources/edit/" + uid
	assert.Contains(t, got.NextSteps, configPageURL)

	link, ok := toolResult.Content[1].(mcp.ResourceLink)
	require.True(t, ok)
	assert.Equal(t, "resource_link", link.Type)
	assert.Equal(t, configPageURL, link.URI)
	assert.Equal(t, name, link.Name)
}

func TestCreateDatasource_SecureFieldsNotLeakedToJSONData(t *testing.T) {
	var capturedJSONData map[string]any

	id := int64(1)
	name := "My Prometheus"
	uid := "prom-uid"
	msg := "ok"
	mockResp := models.AddDataSourceOKBody{
		ID:      &id,
		Name:    &name,
		Message: &msg,
		Datasource: &models.DataSource{ID: id, UID: uid, Name: name, Type: "prometheus"},
	}


	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			JSONData map[string]any `json:"jsonData"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedJSONData = body.JSONData
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResp)
	}))
	defer srv.Close()

	_, err := createDatasource(mockDatasourcesCtx(srv), CreateDatasourceParams{
		Name: name,
		Type: "prometheus",
		Fields: map[string]any{
			"httpMethod":        "GET",
			"basicAuthPassword": "s3cr3t", // secureJsonData field — must be dropped
		},
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

	t.Run("unknown fields are excluded", func(t *testing.T) {
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, schema, map[string]any{"unknownField": "value"})
		assert.NotContains(t, result, "unknownField")
	})

	t.Run("section-prefixed input key is stored under base key", func(t *testing.T) {
		s := &datasourceschemas.DatasourceSchema{
			Fields: []datasourceschemas.DsSchemaField{
				{Key: "region", Section: "aws", Target: "jsonData", ValueType: "string"},
			},
		}
		body := &models.AddDataSourceCommand{}
		result := applyFields(body, s, map[string]any{"aws.region": "us-east-1"})
		assert.Equal(t, "us-east-1", result["region"])
		assert.NotContains(t, result, "aws.region")
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
