//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func snapshotTestContext(t *testing.T, serverURL string) context.Context {
	t.Helper()
	cfg := mcpgrafana.GrafanaConfig{URL: serverURL}
	return mcpgrafana.WithGrafanaConfig(context.Background(), cfg)
}

// --- snapshots_read tool definition and validation ---

func TestSnapshotsReadToolDefinition(t *testing.T) {
	require.NotNil(t, SnapshotsRead)
	assert.Equal(t, "snapshots_read", SnapshotsRead.Tool.Name)
	for _, op := range snapshotReadOperations {
		assert.Contains(t, SnapshotsRead.Tool.Description, op)
	}
}

func TestSnapshotReadParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  SnapshotReadParams
		wantErr string
	}{
		{name: "list needs nothing", params: SnapshotReadParams{Operation: "list"}},
		{
			name:    "get missing key",
			params:  SnapshotReadParams{Operation: "get"},
			wantErr: "key is required for 'get' operation",
		},
		{name: "get with key", params: SnapshotReadParams{Operation: "get", Key: "abc"}},
		{
			name:    "unknown operation",
			params:  SnapshotReadParams{Operation: "bogus"},
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

// --- snapshots_write tool definition and validation ---

func TestSnapshotsWriteToolDefinition(t *testing.T) {
	require.NotNil(t, SnapshotsWrite)
	assert.Equal(t, "snapshots_write", SnapshotsWrite.Tool.Name)
	for _, op := range snapshotWriteOperations {
		assert.Contains(t, SnapshotsWrite.Tool.Description, op)
	}
}

func TestSnapshotWriteParams_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  SnapshotWriteParams
		wantErr string
	}{
		{name: "create with dashboard", params: SnapshotWriteParams{Operation: "create", Dashboard: map[string]any{"title": "x"}}},
		{
			name:    "create missing dashboard",
			params:  SnapshotWriteParams{Operation: "create"},
			wantErr: "dashboard is required for 'create' operation",
		},
		{name: "delete with key", params: SnapshotWriteParams{Operation: "delete", Key: "abc"}},
		{
			name:    "delete missing key",
			params:  SnapshotWriteParams{Operation: "delete"},
			wantErr: "key is required for 'delete' operation",
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

func TestSnapshotWriteParams_Validate_RejectsReadOperations(t *testing.T) {
	// A schema-conformant caller can never send a snapshots_read operation
	// to snapshots_write (its jsonschema enum doesn't offer them), but the
	// Go-level validate() must still reject them explicitly rather than
	// silently succeeding.
	for _, op := range snapshotReadOperations {
		err := SnapshotWriteParams{Operation: op}.validate()
		require.Error(t, err, "operation %q must not validate for snapshots_write", op)
		assert.Contains(t, err.Error(), "snapshots_read operation, not snapshots_write")
	}
}

func TestSnapshotWriteParams_Validate_UnknownOperationListsAllOperations(t *testing.T) {
	err := SnapshotWriteParams{Operation: "bogus"}.validate()
	require.Error(t, err)
	assert.EqualError(t, err, `unknown operation "bogus", must be one of: list, get, create, delete`)
}

func TestSnapshotWriteParams_ValidateExternal(t *testing.T) {
	external := true

	err := SnapshotWriteParams{External: &external, Dashboard: map[string]any{"title": "x"}}.validateExternal()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is required when external is true")

	err = SnapshotWriteParams{External: &external, Key: "abc", Dashboard: map[string]any{"title": "x"}}.validateExternal()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleteKey is required when external is true")

	err = SnapshotWriteParams{External: &external, Key: "abc", DeleteKey: "d1", Dashboard: map[string]any{"title": "x"}}.validateExternal()
	require.NoError(t, err)

	// external unset: no requirement at all.
	require.NoError(t, SnapshotWriteParams{Dashboard: map[string]any{"title": "x"}}.validateExternal())
}

// --- dispatch ---

func TestSnapshotsReadDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := snapshotsRead(context.Background(), SnapshotReadParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshots_read")
}

func TestSnapshotsWriteDispatch_UnknownOperationDoesNotPanic(t *testing.T) {
	_, err := snapshotsWrite(context.Background(), SnapshotWriteParams{Operation: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshots_write")
}

// --- listSnapshots / snapshots_read "list" ---

func TestListSnapshots_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/dashboard/snapshots", r.URL.Path)
		assert.Equal(t, "prod", r.URL.Query().Get("query"))
		assert.Equal(t, "20", r.URL.Query().Get("limit"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"name":"Home","key":"abc","orgId":1,"userId":2,"external":false,"externalUrl":"","expires":"2200-01-01T00:00:00Z","created":"2200-01-01T00:00:00Z","updated":"2200-01-01T00:00:00Z"}]`))
	}))
	t.Cleanup(ts.Close)

	limit := 20
	result, err := listSnapshots(snapshotTestContext(t, ts.URL), ListSnapshotsParams{
		Query: "prod",
		Limit: &limit,
	})
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Home", result[0].Name)
	assert.Equal(t, "abc", result[0].Key)
}

func TestListSnapshots_ErrorOnNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/dashboard/snapshots", r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"access denied"}`))
	}))
	t.Cleanup(ts.Close)

	_, err := listSnapshots(snapshotTestContext(t, ts.URL), ListSnapshotsParams{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 403")
	assert.Contains(t, err.Error(), "access denied")
}

// --- getSnapshot / snapshots_read "get" ---

func TestGetSnapshot_SuccessAndEscapesKey(t *testing.T) {
	var escapedPath string
	var rawQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		escapedPath = r.URL.EscapedPath()
		rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"meta":{"isSnapshot":true},"dashboard":{"title":"Home"}}`))
	}))
	t.Cleanup(ts.Close)

	result, err := getSnapshot(snapshotTestContext(t, ts.URL), GetSnapshotParams{Key: "key?x=1/../../admin"})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "/api/snapshots/key%3Fx=1%2F..%2F..%2Fadmin", escapedPath)
	assert.Empty(t, rawQuery)
	assert.Equal(t, "Home", result.Dashboard["title"])
}

func TestGetSnapshot_RequiresKey(t *testing.T) {
	_, err := getSnapshot(
		mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.com"}),
		GetSnapshotParams{Key: "   "},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot key is required")
}

// --- createSnapshot / snapshots_write "create" ---

func TestCreateSnapshot_SendsBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/snapshots", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "My Snapshot", body["name"])
		assert.NotContains(t, body, "operation", "the consolidated tool's discriminator must not leak into the Grafana API request body")

		dashboard, ok := body["dashboard"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Home", dashboard["title"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deleteKey":"d1","deleteUrl":"http://grafana/api/snapshots-delete/d1","key":"k1","url":"http://grafana/dashboard/snapshot/k1","id":1}`))
	}))
	t.Cleanup(ts.Close)

	result, err := createSnapshot(snapshotTestContext(t, ts.URL), SnapshotWriteParams{
		Name:      "My Snapshot",
		Dashboard: map[string]any{"title": "Home"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "k1", result.Key)
}

func TestCreateSnapshot_RequiresDashboard(t *testing.T) {
	_, err := snapshotsWrite(
		mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.com"}),
		SnapshotWriteParams{Operation: "create"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dashboard is required")
}

func TestCreateSnapshot_ExternalRequiresKeys(t *testing.T) {
	external := true
	_, err := createSnapshot(context.Background(), SnapshotWriteParams{
		External:  &external,
		Dashboard: map[string]any{"title": "Home"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is required when external is true")

	_, err = createSnapshot(
		mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.com"}),
		SnapshotWriteParams{
			External:  &external,
			Key:       "abc",
			Dashboard: map[string]any{"title": "Home"},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleteKey is required when external is true")
}

// --- deleteSnapshot / snapshots_write "delete" ---

func TestDeleteSnapshot_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/snapshots/snap-1", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Snapshot deleted.","id":1}`))
	}))
	t.Cleanup(ts.Close)

	result, err := deleteSnapshot(snapshotTestContext(t, ts.URL), DeleteSnapshotParams{Key: "snap-1"})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message, "Snapshot deleted")
	assert.Equal(t, 1, result.ID)
}

func TestDeleteSnapshot_RequiresKey(t *testing.T) {
	_, err := deleteSnapshot(
		mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.com"}),
		DeleteSnapshotParams{Key: "\t"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot key is required")
}

// --- entrypoint dispatch routes to the right handler ---

func TestSnapshotsReadDispatch_RoutesToTheRightHandler(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/dashboard/snapshots":
			_, _ = w.Write([]byte(`[]`))
		case "/api/snapshots/abc":
			_, _ = w.Write([]byte(`{"meta":{},"dashboard":{}}`))
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(ts.Close)
	ctx := snapshotTestContext(t, ts.URL)

	t.Run("list", func(t *testing.T) {
		_, err := snapshotsRead(ctx, SnapshotReadParams{Operation: "list"})
		require.NoError(t, err)
	})
	t.Run("get", func(t *testing.T) {
		_, err := snapshotsRead(ctx, SnapshotReadParams{Operation: "get", Key: "abc"})
		require.NoError(t, err)
	})
}

func TestSnapshotsWriteDispatch_RoutesToTheRightHandler(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"key":"k1","id":1}`))
		case http.MethodDelete:
			_, _ = w.Write([]byte(`{"message":"deleted","id":1}`))
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	t.Cleanup(ts.Close)
	ctx := snapshotTestContext(t, ts.URL)

	t.Run("create", func(t *testing.T) {
		result, err := snapshotsWrite(ctx, SnapshotWriteParams{Operation: "create", Dashboard: map[string]any{"title": "x"}})
		require.NoError(t, err)
		r, ok := result.(*CreateSnapshotResult)
		require.True(t, ok)
		assert.Equal(t, "k1", r.Key)
	})
	t.Run("delete", func(t *testing.T) {
		result, err := snapshotsWrite(ctx, SnapshotWriteParams{Operation: "delete", Key: "k1"})
		require.NoError(t, err)
		r, ok := result.(*DeleteSnapshotResult)
		require.True(t, ok)
		assert.Equal(t, "deleted", r.Message)
	})
}
