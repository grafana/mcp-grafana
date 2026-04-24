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

// ---- createDatasource ----

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

	toolResult, err := createDatasource(ctx, CreateDatasourceParams{
		Name: name,
		Type: "prometheus",
		URL:  "http://prometheus:9090",
	})
	require.NoError(t, err)
	require.NotNil(t, toolResult)
	assert.False(t, toolResult.IsError)
	require.Len(t, toolResult.Content, 1)

	text, ok := toolResult.Content[0].(mcp.TextContent)
	require.True(t, ok)

	var got CreateDatasourceResult
	require.NoError(t, json.Unmarshal([]byte(text.Text), &got))
	assert.Equal(t, uid, got.UID)
	assert.Equal(t, name, got.Name)
	assert.Equal(t, msg, got.Message)
	assert.Equal(t, id, got.ID)
}

func TestCreateDatasource_CredentialViolation(t *testing.T) {
	tests := []struct {
		name           string
		params         CreateDatasourceParams
		expectedReason string
	}{
		{
			name:           "basicAuth enabled",
			params:         CreateDatasourceParams{Name: "test", Type: "prometheus", BasicAuth: true},
			expectedReason: "basic_auth_enabled_via_mcp_disallowed",
		},
		{
			name:           "basicAuthUser set",
			params:         CreateDatasourceParams{Name: "test", Type: "prometheus", BasicAuthUser: "admin"},
			expectedReason: "basic_auth_user_via_mcp_disallowed",
		},
		{
			name:           "secureJsonData present",
			params:         CreateDatasourceParams{Name: "test", Type: "prometheus", SecureJSONData: map[string]string{"password": "s3cr3t"}},
			expectedReason: "secure_json_data_found",
		},
		{
			name:           "auth intent in URL field",
			params:         CreateDatasourceParams{Name: "test", Type: "prometheus", URL: "add authentication to prometheus datasource"},
			expectedReason: "auth_credential_instructions",
		},
		{
			name:           "embedded AWS key in jsonData",
			params:         CreateDatasourceParams{Name: "test", Type: "prometheus", JSONData: map[string]interface{}{"accessKey": "AKIAIOSFODNN7EXAMPLE"}},
			expectedReason: "embedded_secret_or_token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("credential guard should prevent any HTTP request")
			}))
			defer srv.Close()

			ctx := mockDatasourcesCtx(srv)

			toolResult, err := createDatasource(ctx, tt.params)
			require.NoError(t, err)
			require.NotNil(t, toolResult)
			assert.True(t, toolResult.IsError)

			require.GreaterOrEqual(t, len(toolResult.Content), 1)
			text, ok := toolResult.Content[0].(mcp.TextContent)
			require.True(t, ok)

			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
			assert.Equal(t, "credential_policy_redirect", payload["outcome"])
			assert.Equal(t, tt.expectedReason, payload["reason"])
		})
	}
}

// ---- checkDatasourceCredentials ----

func TestCheckDatasourceCredentials(t *testing.T) {
	tests := []struct {
		name   string
		args   CreateDatasourceParams
		wantOK bool
		reason string
	}{
		{
			name:   "clean params allowed",
			args:   CreateDatasourceParams{Name: "Prometheus", Type: "prometheus", URL: "http://prometheus:9090"},
			wantOK: true,
		},
		{
			name:   "basicAuth true blocked",
			args:   CreateDatasourceParams{Name: "test", Type: "prometheus", BasicAuth: true},
			reason: "basic_auth_enabled_via_mcp_disallowed",
		},
		{
			name:   "basicAuthUser blocked",
			args:   CreateDatasourceParams{Name: "test", Type: "prometheus", BasicAuthUser: "user"},
			reason: "basic_auth_user_via_mcp_disallowed",
		},
		{
			name:   "secureJsonData blocked",
			args:   CreateDatasourceParams{Name: "test", Type: "prometheus", SecureJSONData: map[string]string{"token": "abc"}},
			reason: "secure_json_data_found",
		},
		{
			name:   "auth intent in name blocked",
			args:   CreateDatasourceParams{Name: "add authentication to grafana datasource", Type: "prometheus"},
			reason: "auth_credential_instructions",
		},
		{
			name:   "auth intent in database field blocked",
			args:   CreateDatasourceParams{Name: "test", Type: "postgres", Database: "configure credentials with username and password"},
			reason: "auth_credential_instructions",
		},
		{
			name:   "private key in jsonData blocked",
			args:   CreateDatasourceParams{Name: "test", Type: "prometheus", JSONData: map[string]interface{}{"key": "-----BEGIN RSA PRIVATE KEY-----"}},
			reason: "embedded_secret_or_token",
		},
		{
			name:   "bearer token in URL blocked",
			args:   CreateDatasourceParams{Name: "test", Type: "prometheus", URL: "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature123"},
			reason: "embedded_secret_or_token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkDatasourceCredentials(tt.args)
			if tt.wantOK {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.reason, got)
			}
		})
	}
}

// ---- datasourceConfigPageURL ----

func makeURLTestCtx(grafanaURL, publicURL string) context.Context {
	cfg := mcpgrafana.GrafanaConfig{URL: grafanaURL}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
	if publicURL != "" {
		ctx = mcpgrafana.WithGrafanaClient(ctx, &mcpgrafana.GrafanaClient{PublicURL: publicURL})
	}
	return ctx
}

func TestDatasourceConfigPageURL(t *testing.T) {
	tests := []struct {
		name       string
		grafanaURL string
		publicURL  string
		uid        string
		want       string
	}{
		{
			name:       "no uid → new page",
			grafanaURL: "http://localhost:3000",
			want:       "http://localhost:3000/connections/datasources/new",
		},
		{
			name:       "uid → edit page",
			grafanaURL: "http://localhost:3000",
			uid:        "abc-123",
			want:       "http://localhost:3000/connections/datasources/edit/abc-123",
		},
		{
			name:       "uid with slashes is path-escaped",
			grafanaURL: "http://localhost:3000",
			uid:        "prom/uid",
			want:       "http://localhost:3000/connections/datasources/edit/prom%2Fuid",
		},
		{
			name:       "prefers public URL over config URL",
			grafanaURL: "http://internal:3000",
			publicURL:  "https://grafana.example.com",
			want:       "https://grafana.example.com/connections/datasources/new",
		},
		{
			name:       "https config URL supported",
			grafanaURL: "https://grafana.example.com",
			want:       "https://grafana.example.com/connections/datasources/new",
		},
		{
			name: "empty URL returns empty string",
			want: "",
		},
		{
			name:       "invalid scheme returns empty string",
			grafanaURL: "ftp://grafana.example.com",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := makeURLTestCtx(tt.grafanaURL, tt.publicURL)
			got := datasourceConfigPageURL(ctx, tt.uid)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---- credentialViolationResult ----

func TestCredentialViolationResult(t *testing.T) {
	t.Run("with config URL has text and resource link", func(t *testing.T) {
		configURL := "https://grafana.example.com/connections/datasources/new"
		result := credentialViolationResult("some_reason", configURL)

		assert.True(t, result.IsError)
		require.Len(t, result.Content, 2)

		text, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
		assert.Equal(t, "credential_policy_redirect", payload["outcome"])
		assert.Equal(t, "some_reason", payload["reason"])
		assert.Equal(t, configURL, payload["open_config_page_url"])

		link, ok := result.Content[1].(mcp.ResourceLink)
		require.True(t, ok)
		assert.Equal(t, configURL, link.URI)
	})

	t.Run("without config URL has text only", func(t *testing.T) {
		result := credentialViolationResult("some_reason", "")

		assert.True(t, result.IsError)
		require.Len(t, result.Content, 1)
		_, ok := result.Content[0].(mcp.TextContent)
		assert.True(t, ok)
	})
}

// ---- addAuthenticationToDatasource ----

func TestAddAuthenticationToDatasource(t *testing.T) {
	// The tool always returns a credential_policy_redirect to steer users to the UI.

	t.Run("no uid redirects to new datasource page", func(t *testing.T) {
		ctx := makeURLTestCtx("http://grafana:3000", "")
		result, err := addAuthenticationToDatasource(ctx, AddAuthenticationToDatasourceParams{})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		require.Len(t, result.Content, 2)

		text, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
		assert.Equal(t, "credential_policy_redirect", payload["outcome"])
		assert.Equal(t, "auth_credential_instructions", payload["reason"])
		assert.Equal(t, "http://grafana:3000/connections/datasources/new", payload["open_config_page_url"])

		link, ok := result.Content[1].(mcp.ResourceLink)
		require.True(t, ok)
		assert.Equal(t, "http://grafana:3000/connections/datasources/new", link.URI)
	})

	t.Run("valid uid redirects to edit datasource page", func(t *testing.T) {
		ctx := makeURLTestCtx("http://grafana:3000", "")
		result, err := addAuthenticationToDatasource(ctx, AddAuthenticationToDatasourceParams{UID: "prometheus"})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		require.Len(t, result.Content, 2)

		text, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
		assert.Equal(t, "credential_policy_redirect", payload["outcome"])
		assert.Equal(t, "auth_credential_instructions", payload["reason"])
		assert.Equal(t, "http://grafana:3000/connections/datasources/edit/prometheus", payload["open_config_page_url"])

		link, ok := result.Content[1].(mcp.ResourceLink)
		require.True(t, ok)
		assert.Equal(t, "http://grafana:3000/connections/datasources/edit/prometheus", link.URI)
	})

	t.Run("secret-like uid short-circuits with embedded_secret_or_token reason", func(t *testing.T) {
		ctx := makeURLTestCtx("http://grafana:3000", "")
		result, err := addAuthenticationToDatasource(ctx, AddAuthenticationToDatasourceParams{UID: "ghp_abcdefghijklmnopqrstuvwxyz1234567890"})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)

		text, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
		assert.Equal(t, "credential_policy_redirect", payload["outcome"])
		assert.Equal(t, "embedded_secret_or_token", payload["reason"])
	})

	t.Run("auth-intent uid short-circuits with auth_credential_instructions reason", func(t *testing.T) {
		ctx := makeURLTestCtx("http://grafana:3000", "")
		result, err := addAuthenticationToDatasource(ctx, AddAuthenticationToDatasourceParams{UID: "add authentication"})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)

		text, ok := result.Content[0].(mcp.TextContent)
		require.True(t, ok)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(text.Text), &payload))
		assert.Equal(t, "credential_policy_redirect", payload["outcome"])
		assert.Equal(t, "auth_credential_instructions", payload["reason"])
	})

	t.Run("no grafana url returns text only without resource link", func(t *testing.T) {
		ctx := makeURLTestCtx("", "")
		result, err := addAuthenticationToDatasource(ctx, AddAuthenticationToDatasourceParams{UID: "prometheus"})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		require.Len(t, result.Content, 1)

		_, ok := result.Content[0].(mcp.TextContent)
		assert.True(t, ok)
	})
}
