//go:build unit
// +build unit

package tools

import (
	"context"
	"testing"

	"github.com/grafana/incident-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- incidents_read tool definition and validation ---

func TestIncidentsReadToolDefinition(t *testing.T) {
	require.NotNil(t, IncidentsRead)
	assert.Equal(t, "incidents_read", IncidentsRead.Tool.Name)
	for _, op := range incidentReadOperations {
		assert.Contains(t, IncidentsRead.Tool.Description, op)
	}
}

func TestIncidentReadParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  IncidentReadParams
		wantErr string
	}{
		{name: "list needs nothing", params: IncidentReadParams{Operation: "list"}},
		{
			name:    "get missing incidentId",
			params:  IncidentReadParams{Operation: "get"},
			wantErr: "incidentId is required for 'get' operation",
		},
		{name: "get with incidentId", params: IncidentReadParams{Operation: "get", IncidentID: "abc"}},
		{
			name:    "unknown operation",
			params:  IncidentReadParams{Operation: "bogus"},
			wantErr: `unknown operation "bogus", must be one of: list, get`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// --- incidents_write tool definition and validation ---

func TestIncidentsWriteToolDefinition(t *testing.T) {
	require.NotNil(t, IncidentsWrite)
	assert.Equal(t, "incidents_write", IncidentsWrite.Tool.Name)
	for _, op := range incidentWriteOperations {
		assert.Contains(t, IncidentsWrite.Tool.Description, op)
	}
}

func TestIncidentWriteParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  IncidentWriteParams
		wantErr string
	}{
		{
			name:   "create with required fields",
			params: IncidentWriteParams{Operation: "create", Title: "t", Severity: "minor", RoomPrefix: "test"},
		},
		{
			name:    "create missing title",
			params:  IncidentWriteParams{Operation: "create", Severity: "minor", RoomPrefix: "test"},
			wantErr: "title is required for 'create' operation",
		},
		{
			name:    "create missing severity",
			params:  IncidentWriteParams{Operation: "create", Title: "t", RoomPrefix: "test"},
			wantErr: "severity is required for 'create' operation",
		},
		{
			name:    "create missing roomPrefix",
			params:  IncidentWriteParams{Operation: "create", Title: "t", Severity: "minor"},
			wantErr: "roomPrefix is required for 'create' operation",
		},
		{name: "update with incidentId", params: IncidentWriteParams{Operation: "update", IncidentID: "abc", Status: "resolved"}},
		{
			name:    "update missing incidentId",
			params:  IncidentWriteParams{Operation: "update", Status: "resolved"},
			wantErr: "incidentId is required for 'update' operation",
		},
		{
			name:   "add_activity with required fields",
			params: IncidentWriteParams{Operation: "add_activity", IncidentID: "abc", Body: "note"},
		},
		{
			name:    "add_activity missing incidentId",
			params:  IncidentWriteParams{Operation: "add_activity", Body: "note"},
			wantErr: "incidentId is required for 'add_activity' operation",
		},
		{
			name:    "add_activity missing body",
			params:  IncidentWriteParams{Operation: "add_activity", IncidentID: "abc"},
			wantErr: "body is required for 'add_activity' operation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestIncidentWriteParams_Validate_RejectsReadOperations(t *testing.T) {
	// A schema-conformant caller can never send an incidents_read operation
	// to incidents_write (its jsonschema enum doesn't offer them), but the
	// Go-level validate() must still reject them explicitly rather than
	// silently succeeding.
	for _, op := range incidentReadOperations {
		err := IncidentWriteParams{Operation: op}.validate()
		require.Error(t, err, "operation %q must not validate for incidents_write", op)
		assert.Contains(t, err.Error(), "incidents_read operation, not incidents_write")
	}
}

func TestIncidentWriteParams_Validate_UnknownOperationListsAllOperations(t *testing.T) {
	err := IncidentWriteParams{Operation: "bogus"}.validate()
	require.Error(t, err)
	assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, get, create, update, add_activity`)
}

// --- dispatch ---

func TestIncidentsReadDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := incidentsRead(context.Background(), IncidentReadParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incidents_read")
}

func TestIncidentsWriteDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := incidentsWrite(context.Background(), IncidentWriteParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "incidents_write")
}

func TestIncidentsReadDispatch_RoutesToTheRightHandler(t *testing.T) {
	ctx := newIncidentTestContext()

	t.Run("list", func(t *testing.T) {
		result, err := incidentsRead(ctx, IncidentReadParams{Operation: "list", Limit: 2})
		require.NoError(t, err)
		r, ok := result.(*ListIncidentsResult)
		require.True(t, ok)
		assert.Len(t, r.Incidents, 2)
	})

	t.Run("get", func(t *testing.T) {
		result, err := incidentsRead(ctx, IncidentReadParams{Operation: "get", IncidentID: "1"})
		require.NoError(t, err)
		_, ok := result.(*incident.Incident)
		require.True(t, ok)
	})
}

func TestIncidentsWriteDispatch_RoutesToTheRightHandler(t *testing.T) {
	ctx := newIncidentTestContext()

	t.Run("create", func(t *testing.T) {
		result, err := incidentsWrite(ctx, IncidentWriteParams{
			Operation:  "create",
			Title:      "high latency in web requests",
			Severity:   "minor",
			RoomPrefix: "test",
			IsDrill:    true,
			Status:     "active",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("update", func(t *testing.T) {
		result, err := incidentsWrite(ctx, IncidentWriteParams{
			Operation:  "update",
			IncidentID: "incident-123",
			Status:     "resolved",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("add_activity", func(t *testing.T) {
		result, err := incidentsWrite(ctx, IncidentWriteParams{
			Operation:  "add_activity",
			IncidentID: "123",
			Body:       "note",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
	})
}
