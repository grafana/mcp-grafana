//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockAssertsKGServer(handler http.HandlerFunc) (*httptest.Server, context.Context) {
	srv := httptest.NewServer(handler)
	config := mcpgrafana.GrafanaConfig{
		URL:    srv.URL,
		APIKey: "test-api-key",
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), config)
	return srv, ctx
}

func TestGetGraphSchema(t *testing.T) {
	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/plugins/grafana-asserts-app/resources/asserts/api-server/v1/entity_type", r.URL.Path)
		require.Equal(t, "GET", r.Method)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		resp := `{
			"entities": [
				{
					"entityType": "Service",
					"name": "Service",
					"properties": [
						{"name": "job", "type": "string"},
						{"name": "namespace", "type": "string"}
					],
					"connectedEntityTypes": ["Pod", "Node", "Database"],
					"active": true
				},
				{
					"entityType": "Pod",
					"name": "Pod",
					"properties": [
						{"name": "pod", "type": "string"}
					],
					"connectedEntityTypes": ["Service", "Node"],
					"active": true
				},
				{
					"entityType": "OldType",
					"name": "OldType",
					"properties": [],
					"connectedEntityTypes": [],
					"active": false
				}
			]
		}`
		_, _ = w.Write([]byte(resp))
	})
	defer srv.Close()

	result, err := getGraphSchema(ctx, GetGraphSchemaParams{})
	require.NoError(t, err)

	var parsed struct {
		EntityTypes []schemaEntityType `json:"entityTypes"`
	}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	assert.Len(t, parsed.EntityTypes, 2)
	assert.Equal(t, "Service", parsed.EntityTypes[0].Type)
	assert.Equal(t, []string{"job", "namespace"}, parsed.EntityTypes[0].Properties)
	assert.Equal(t, []string{"Pod", "Node", "Database"}, parsed.EntityTypes[0].ConnectedTypes)
	assert.Equal(t, "Pod", parsed.EntityTypes[1].Type)
}

func TestSearchEntities(t *testing.T) {
	startTime := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 2, 25, 11, 0, 0, 0, time.UTC)

	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/plugins/grafana-asserts-app/resources/asserts/api-server/v1/search", r.URL.Path)
		require.Equal(t, "POST", r.Method)

		var body searchRequestDTO
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, startTime.UnixMilli(), body.TimeCriteria.Start)
		assert.Equal(t, endTime.UnixMilli(), body.TimeCriteria.End)
		require.Len(t, body.FilterCriteria, 1)
		assert.Equal(t, "Service", body.FilterCriteria[0].EntityType)
		assert.True(t, body.FilterCriteria[0].HavingAssertion)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"entities": [{"type": "Service", "name": "checkout"}]}`))
	})
	defer srv.Close()

	result, err := searchEntities(ctx, SearchEntitiesParams{
		EntityType:    "Service",
		HasAssertions: true,
		StartTime:     startTime,
		EndTime:       endTime,
	})
	require.NoError(t, err)
	assert.Contains(t, result, "checkout")
}

func TestSearchEntitiesWithScope(t *testing.T) {
	startTime := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 2, 25, 11, 0, 0, 0, time.UTC)

	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		var body searchRequestDTO
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		require.NotNil(t, body.ScopeCriteria)
		assert.Equal(t, []string{"production"}, body.ScopeCriteria.NameAndValues["env"])
		assert.Equal(t, []string{"us-east"}, body.ScopeCriteria.NameAndValues["site"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"entities": []}`))
	})
	defer srv.Close()

	_, err := searchEntities(ctx, SearchEntitiesParams{
		EntityType: "Service",
		Env:        "production",
		Site:       "us-east",
		StartTime:  startTime,
		EndTime:    endTime,
	})
	require.NoError(t, err)
}

func TestSearchEntitiesWithSearchText(t *testing.T) {
	startTime := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 2, 25, 11, 0, 0, 0, time.UTC)

	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		var body searchRequestDTO
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		require.Len(t, body.FilterCriteria[0].PropertyMatchers, 1)
		assert.Equal(t, "name", body.FilterCriteria[0].PropertyMatchers[0].Name)
		assert.Equal(t, "checkout", body.FilterCriteria[0].PropertyMatchers[0].Value)
		assert.Equal(t, "CONTAINS", body.FilterCriteria[0].PropertyMatchers[0].Op)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"entities": [{"type": "Service", "name": "checkout-service"}]}`))
	})
	defer srv.Close()

	result, err := searchEntities(ctx, SearchEntitiesParams{
		EntityType: "Service",
		SearchText: "checkout",
		StartTime:  startTime,
		EndTime:    endTime,
	})
	require.NoError(t, err)
	assert.Contains(t, result, "checkout-service")
}

func TestGetEntity(t *testing.T) {
	t.Run("slim output", func(t *testing.T) {
		srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "GET", r.Method)
			require.Contains(t, r.URL.Path, "/v1/entity/info")
			assert.Equal(t, "Service", r.URL.Query().Get("entity_type"))
			assert.Equal(t, "checkout", r.URL.Query().Get("entity_name"))
			assert.Equal(t, "production", r.URL.Query().Get("entityEnv"))

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": 42,
				"type": "Service",
				"name": "checkout",
				"active": true,
				"env": "production",
				"namespace": "default",
				"connectedEntityTypes": {"Pod": 3, "Node": 2},
				"properties": {"job": "checkout", "instance": "10.0.0.1:8080"},
				"assertionCount": 5
			}`))
		})
		defer srv.Close()

		result, err := getEntity(ctx, GetEntityParams{
			EntityType: "Service",
			EntityName: "checkout",
			Env:        "production",
		})
		require.NoError(t, err)

		var slim slimEntity
		require.NoError(t, json.Unmarshal([]byte(result), &slim))
		assert.Equal(t, "Service", slim.Type)
		assert.Equal(t, "checkout", slim.Name)
		assert.Equal(t, "production", slim.Env)
		assert.Equal(t, 5, slim.AssertionCount)
		assert.Equal(t, map[string]int{"Pod": 3, "Node": 2}, slim.ConnectedTypes)
		assert.Nil(t, slim.Properties)
	})

	t.Run("detailed output", func(t *testing.T) {
		srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"id": 42,
				"type": "Service",
				"name": "checkout",
				"active": true,
				"properties": {"job": "checkout", "instance": "10.0.0.1:8080"}
			}`))
		})
		defer srv.Close()

		result, err := getEntity(ctx, GetEntityParams{
			EntityType: "Service",
			EntityName: "checkout",
			Detailed:   true,
		})
		require.NoError(t, err)
		assert.Contains(t, result, `"instance"`)
		assert.Contains(t, result, `"10.0.0.1:8080"`)
	})
}

func TestGetConnectedEntities(t *testing.T) {
	callCount := 0
	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if callCount == 1 {
			require.Contains(t, r.URL.Path, "/v1/entity/info")
			_, _ = w.Write([]byte(`{"id": 42, "type": "Service", "name": "checkout", "active": true}`))
			return
		}

		require.Contains(t, r.URL.Path, "/public/v1/entities/Service/42/connected")
		assert.Equal(t, "Pod", r.URL.Query().Get("type"))
		assert.Equal(t, "10", r.URL.Query().Get("pagination.limit"))

		_, _ = w.Write([]byte(`{
			"items": [
				{"id": "100", "type": "Pod", "name": "checkout-abc123", "active": true, "scope": {"namespace": "default"}},
				{"id": "101", "type": "Pod", "name": "checkout-def456", "active": true, "scope": {"namespace": "default"}}
			],
			"pagination": {"limit": 10, "offset": 0}
		}`))
	})
	defer srv.Close()

	result, err := getConnectedEntities(ctx, GetConnectedEntitiesParams{
		EntityType:    "Service",
		EntityName:    "checkout",
		ConnectedType: "Pod",
		Limit:         10,
	})
	require.NoError(t, err)

	var parsed struct {
		Source    slimEntity   `json:"source"`
		Connected []slimEntity `json:"connected"`
	}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	assert.Equal(t, "Service", parsed.Source.Type)
	assert.Equal(t, "checkout", parsed.Source.Name)
	assert.Len(t, parsed.Connected, 2)
	assert.Equal(t, "Pod", parsed.Connected[0].Type)
	assert.Equal(t, "checkout-abc123", parsed.Connected[0].Name)
}

func TestListEntities(t *testing.T) {
	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "GET", r.Method)
		require.Contains(t, r.URL.Path, "/public/v1/entities/Service")
		assert.Equal(t, "eq:production", r.URL.Query().Get("scope.env"))
		assert.Equal(t, "25", r.URL.Query().Get("pagination.limit"))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"items": [
				{"id": "1", "type": "Service", "name": "api-gateway", "active": true, "scope": {"env": "production"}},
				{"id": "2", "type": "Service", "name": "checkout", "active": true, "scope": {"env": "production"}}
			],
			"pagination": {"limit": 25, "offset": 0}
		}`))
	})
	defer srv.Close()

	result, err := listEntities(ctx, ListEntitiesParams{
		EntityType: "Service",
		Env:        "eq:production",
	})
	require.NoError(t, err)

	var parsed struct {
		Entities   []slimEntity `json:"entities"`
		Pagination struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		} `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	assert.Len(t, parsed.Entities, 2)
	assert.Equal(t, "api-gateway", parsed.Entities[0].Name)
}

func TestCountEntities(t *testing.T) {
	startTime := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 2, 25, 11, 0, 0, 0, time.UTC)

	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/plugins/grafana-asserts-app/resources/asserts/api-server/v1/entity_type/count", r.URL.Path)
		require.Equal(t, "POST", r.Method)

		var body entityCountRequestDTO
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, startTime.UnixMilli(), body.TimeCriteria.Start)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Service": 42, "Pod": 128, "Node": 5}`))
	})
	defer srv.Close()

	result, err := countEntities(ctx, CountEntitiesParams{
		StartTime: startTime,
		EndTime:   endTime,
	})
	require.NoError(t, err)

	var counts map[string]int
	require.NoError(t, json.Unmarshal([]byte(result), &counts))
	assert.Equal(t, 42, counts["Service"])
	assert.Equal(t, 128, counts["Pod"])
	assert.Equal(t, 5, counts["Node"])
}

func TestGetAssertionSummary(t *testing.T) {
	startTime := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 2, 25, 11, 0, 0, 0, time.UTC)

	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/plugins/grafana-asserts-app/resources/asserts/api-server/v1/assertions/summary", r.URL.Path)
		require.Equal(t, "POST", r.Method)

		var body assertionsSummaryRequestDTO
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, startTime.UnixMilli(), body.StartTime)
		require.Len(t, body.EntityKeys, 1)
		assert.Equal(t, "Service", body.EntityKeys[0].Type)
		assert.Equal(t, "checkout", body.EntityKeys[0].Name)
		assert.Equal(t, "production", body.EntityKeys[0].Scope["env"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"summaries": [{"entityType": "Service", "totalCount": 3}]}`))
	})
	defer srv.Close()

	result, err := getAssertionSummary(ctx, GetAssertionSummaryParams{
		EntityType: "Service",
		EntityName: "checkout",
		Env:        "production",
		StartTime:  startTime,
		EndTime:    endTime,
	})
	require.NoError(t, err)
	assert.Contains(t, result, "totalCount")
}

func TestSearchRcaPatterns(t *testing.T) {
	startTime := time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC)
	endTime := time.Date(2026, 2, 25, 11, 0, 0, 0, time.UTC)

	srv, ctx := setupMockAssertsKGServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/plugins/grafana-asserts-app/resources/asserts/api-server/v1/patterns/search", r.URL.Path)
		require.Equal(t, "POST", r.Method)

		var body rcaPatternSearchRequestDTO
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "Service", body.EntityType)
		assert.Equal(t, "checkout", body.EntityName)
		assert.Equal(t, startTime.UnixMilli(), body.Start)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"patterns": [{"name": "HighLatency", "rootCause": "Database"}]}`))
	})
	defer srv.Close()

	result, err := searchRcaPatterns(ctx, SearchRcaPatternsParams{
		EntityType: "Service",
		EntityName: "checkout",
		StartTime:  startTime,
		EndTime:    endTime,
	})
	require.NoError(t, err)
	assert.Contains(t, result, "HighLatency")
}
