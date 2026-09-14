//go:build unit
// +build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	irmclient "github.com/grafana/gcx/client/irm"
	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIncidentTestContext() context.Context {
	client := incident.NewTestClient()
	return mcpgrafana.WithIncidentClient(context.Background(), client)
}

// newIRMTestContext serves a QueryIncidentPreviews response built from previews,
// and records the decoded request body of the last call into gotRequest.
func newIRMTestContext(t *testing.T, previews []map[string]any, gotRequest *map[string]any) context.Context {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotRequest != nil {
			require.NoError(t, json.NewDecoder(r.Body).Decode(gotRequest))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"incidentPreviews": previews,
			"cursor":           map[string]any{"hasMore": false},
		})
	}))
	t.Cleanup(srv.Close)

	return mcpgrafana.WithIRMClient(context.Background(), irmclient.NewIncidentClient(srv.Client(), srv.URL))
}

func TestIncidentTools(t *testing.T) {
	t.Run("list incidents", func(t *testing.T) {
		var req map[string]any
		ctx := newIRMTestContext(t, []map[string]any{
			{
				"incidentID":    "1",
				"title":         "high latency",
				"status":        "active",
				"severityLabel": "Minor",
				"createdTime":   "2025-04-23T10:00:00Z",
				"modifiedTime":  "2025-04-23T10:05:00Z",
				"incidentStart": "2025-04-23T09:55:00Z",
			},
			{"incidentID": "2", "title": "errors", "status": "resolved"},
		}, &req)

		result, err := listIncidents(ctx, ListIncidentsParams{Limit: 2})
		require.NoError(t, err)
		require.Len(t, result.Incidents, 2)
		assert.Equal(t, "1", result.Incidents[0].IncidentID)
		assert.Equal(t, "Minor", result.Incidents[0].Severity)
		assert.Equal(t, "2025-04-23T10:00:00Z", result.Incidents[0].CreatedTime)
		assert.Equal(t, "2025-04-23T09:55:00Z", result.Incidents[0].IncidentStart)
		assert.Empty(t, result.Incidents[1].CreatedTime)
		assert.False(t, result.HasMore)

		query := req["query"].(map[string]any)
		assert.Equal(t, "isdrill:false", query["queryString"])
		assert.Equal(t, "DESC", query["orderDirection"])
	})

	t.Run("list incidents derives hasMore", func(t *testing.T) {
		ctx := newIRMTestContext(t, []map[string]any{
			{"incidentID": "1", "title": "a"},
			{"incidentID": "2", "title": "b"},
			{"incidentID": "3", "title": "c"},
		}, nil)

		result, err := listIncidents(ctx, ListIncidentsParams{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, result.Incidents, 2)
		assert.True(t, result.HasMore)
	})

	t.Run("list incidents passes the status filter through", func(t *testing.T) {
		var req map[string]any
		ctx := newIRMTestContext(t, nil, &req)

		_, err := listIncidents(ctx, ListIncidentsParams{Drill: true, Status: "resolved"})
		require.NoError(t, err)
		assert.Equal(t, " status:resolved", req["query"].(map[string]any)["queryString"])
	})

	t.Run("list incidents with no client configured", func(t *testing.T) {
		_, err := listIncidents(context.Background(), ListIncidentsParams{})
		require.Error(t, err)
	})

	t.Run("create incident", func(t *testing.T) {
		ctx := newIncidentTestContext()
		result, err := createIncident(ctx, CreateIncidentParams{
			Title:         "high latency in web requests",
			Severity:      "minor",
			RoomPrefix:    "test",
			IsDrill:       true,
			Status:        "active",
			AttachCaption: "Test attachment",
			AttachURL:     "https://grafana.com",
		})
		require.NoError(t, err)
		assert.Equal(t, "high latency in web requests", result.Title)
		assert.Equal(t, "minor", result.Severity)
		assert.True(t, result.IsDrill)
		assert.Equal(t, "active", result.Status)
	})

	t.Run("add activity to incident", func(t *testing.T) {
		ctx := newIncidentTestContext()
		result, err := addActivityToIncident(ctx, AddActivityToIncidentParams{
			IncidentID: "123",
			Body:       "The incident was created by user-123",
			EventTime:  "2021-08-07T11:58:23Z",
		})
		require.NoError(t, err)
		assert.Equal(t, "The incident was created by user-123", result.Body)
		assert.Equal(t, "2021-08-07T11:58:23Z", result.EventTime)
	})
}
