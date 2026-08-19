//go:build unit
// +build unit

package tools

import (
	"context"
	"testing"

	"github.com/grafana/incident-go"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newIncidentTestContext() context.Context {
	client := incident.NewTestClient()
	return mcpgrafana.WithIncidentClient(context.Background(), client)
}

func TestIncidentTools(t *testing.T) {
	t.Run("list incidents", func(t *testing.T) {
		ctx := newIncidentTestContext()
		result, err := listIncidents(ctx, ListIncidentsParams{
			Limit: 2,
		})
		require.NoError(t, err)
		assert.Len(t, result.Incidents, 2)
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

	t.Run("update incident", func(t *testing.T) {
		ctx := newIncidentTestContext()
		result, err := updateIncident(ctx, UpdateIncidentParams{
			IncidentID: "incident-123",
			Status:     "resolved",
			Severity:   "major",
			Title:      "high latency in web requests",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "incident-123", result.IncidentID)
	})

	t.Run("update incident requires an incident ID", func(t *testing.T) {
		ctx := newIncidentTestContext()
		_, err := updateIncident(ctx, UpdateIncidentParams{
			Status: "resolved",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "incidentId is required")
	})

	t.Run("update incident requires at least one field", func(t *testing.T) {
		ctx := newIncidentTestContext()
		_, err := updateIncident(ctx, UpdateIncidentParams{
			IncidentID: "incident-123",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at least one of status, severity or title")
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
