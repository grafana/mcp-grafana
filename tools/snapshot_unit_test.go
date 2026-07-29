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

func TestCreateSnapshot_SendsBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/snapshots", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "My Snapshot", body["name"])

		dashboard, ok := body["dashboard"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Home", dashboard["title"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deleteKey":"d1","deleteUrl":"http://grafana/api/snapshots-delete/d1","key":"k1","url":"http://grafana/dashboard/snapshot/k1","id":1}`))
	}))
	t.Cleanup(ts.Close)

	result, err := createSnapshot(snapshotTestContext(t, ts.URL), CreateSnapshotParams{
		Name:      "My Snapshot",
		Dashboard: map[string]any{"title": "Home"},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "k1", result.Key)
}

func TestCreateSnapshot_RequiresDashboard(t *testing.T) {
	_, err := createSnapshot(
		mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.com"}),
		CreateSnapshotParams{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dashboard is required")
}

func TestCreateSnapshot_ExternalRequiresKeys(t *testing.T) {
	external := true
	_, err := createSnapshot(context.Background(), CreateSnapshotParams{
		External:  &external,
		Dashboard: map[string]any{"title": "Home"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grafana URL is not configured")

	_, err = createSnapshot(
		mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.com"}),
		CreateSnapshotParams{
			External:  &external,
			Dashboard: map[string]any{"title": "Home"},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is required when external is true")
}

func TestCreateSnapshot_ExternalRequiresDeleteKey(t *testing.T) {
	external := true
	_, err := createSnapshot(
		mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.com"}),
		CreateSnapshotParams{
			External:  &external,
			Key:       "abc",
			Dashboard: map[string]any{"title": "Home"},
		},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleteKey is required when external is true")
}

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
