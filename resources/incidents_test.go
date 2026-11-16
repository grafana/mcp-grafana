//go:build unit

package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/incident-go"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// connectIncidents serves ActiveIncidents against a fake incident API, with
// the incident client built as in stdio mode.
func connectIncidents(t *testing.T, previews []incident.IncidentPreview) *mcp.ClientSession {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/plugins/grafana-irm-app/resources/api/v1/IncidentsService.QueryIncidentPreviews":
			_ = json.NewEncoder(w).Encode(incident.QueryIncidentPreviewsResponse{IncidentPreviews: previews})
		case "/api/plugins/grafana-irm-app/resources/api/v1/IncidentsService.GetIncident":
			var req incident.GetIncidentRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.IncidentID != "1" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"incidents.not_found","error":"Incident not found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(incident.GetIncidentResponse{Incident: incident.Incident{IncidentID: "1", Title: "checkout is down"}})
		case "/api/plugins/grafana-irm-app/resources/api/v1/FieldsService.GetFieldValues",
			"/api/plugins/grafana-irm-app/resources/api/v1/FieldsService.GetFields":
			// Reads also resolve custom fields; there are none here.
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("GRAFANA_URL", server.URL)
	ctx := mcpgrafana.ExtractIncidentClientFromEnv(mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: server.URL}))

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	Register(s, ActiveIncidents)
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(_ context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(ctx, method, req)
		}
	})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() { _ = s.Run(context.Background(), serverTransport) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "0"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// Only incidents that started in the last 30 days are listed; one without a
// start time falls back to when it was created, and one with no usable time
// is kept rather than hidden.
func TestListActiveIncidents(t *testing.T) {
	ago := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	session := connectIncidents(t, []incident.IncidentPreview{
		{IncidentID: "1", Title: "checkout is down", SeverityLabel: "Critical", IncidentStart: ago(time.Hour)},
		{IncidentID: "2", Title: "slow logins", CreatedTime: ago(10 * 24 * time.Hour)},
		{IncidentID: "3", Title: "forgotten", SeverityLabel: "Minor", IncidentStart: ago(400 * 24 * time.Hour)},
		{IncidentID: "4", Title: "no times"},
	})

	result, err := session.ListResources(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, result.Resources, 3)
	assert.Equal(t, "grafana://irm/incidents/1", result.Resources[0].URI)
	assert.Equal(t, "checkout is down · active incident (Critical)", result.Resources[0].Description)
	assert.Equal(t, "grafana://irm/incidents/2", result.Resources[1].URI)
	assert.Equal(t, "slow logins · active incident", result.Resources[1].Description)
	assert.Equal(t, "grafana://irm/incidents/4", result.Resources[2].URI)
}

func TestReadIncidentResource(t *testing.T) {
	session := connectIncidents(t, nil)

	result, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "grafana://irm/incidents/1"})
	require.NoError(t, err)
	assert.Equal(t, "private", result.CacheScope)
	assert.Contains(t, result.Contents[0].Text, `"title":"checkout is down"`)

	_, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "grafana://irm/incidents/missing"})
	var rpcErr *jsonrpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, int64(mcp.CodeResourceNotFound), rpcErr.Code)
}
