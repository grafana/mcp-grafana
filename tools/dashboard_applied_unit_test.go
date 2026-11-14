//go:build unit

package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appliedEchoServer serves one classic dashboard through the legacy API and
// accepts any save.
func appliedEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/dashboards/uid/dash-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"dashboard": map[string]interface{}{
					"id": 7, "uid": "dash-1", "title": "Before", "version": 3,
					"panels": []interface{}{map[string]interface{}{"id": 1, "title": "CPU", "description": "old"}},
				},
				"meta": map[string]interface{}{"folderUid": "folder-1"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/dashboards/db":
			_, _ = w.Write([]byte(`{"id":7,"uid":"dash-1","status":"success","version":4,"url":"/d/dash-1/dash"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestUpdateDashboard_PatchResultEchoesAppliedOperations(t *testing.T) {
	ops := []PatchOperation{
		{Op: "replace", Path: "$.title", Value: "After"},
		{Op: "add", Path: "$.panels[0].interval", Value: map[string]interface{}{"every": "1m"}},
		{Op: "remove", Path: "$.panels[0].description"},
	}
	result, err := updateDashboard(mockCtxWithClient(appliedEchoServer(t)), UpdateDashboardParams{UID: "dash-1", Operations: ops})
	require.NoError(t, err)

	require.Equal(t, []AppliedOperation{
		{Op: "replace", Path: "$.title", Value: "After"},
		{Op: "add", Path: "$.panels[0].interval", Value: map[string]interface{}{"every": "1m"}},
		{Op: "remove", Path: "$.panels[0].description"},
	}, result.Applied)

	// The save response keeps its place at the top level of the result;
	// 'applied' is added next to it.
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	assert.JSONEq(t, `"dash-1"`, string(fields["uid"]))
	assert.JSONEq(t, `"success"`, string(fields["status"]))
	assert.JSONEq(t, `4`, string(fields["version"]))
	assert.JSONEq(t, `[
		{"op":"replace","path":"$.title","value":"After"},
		{"op":"add","path":"$.panels[0].interval","value":{"every":"1m"}},
		{"op":"remove","path":"$.panels[0].description"}
	]`, string(fields["applied"]))
}

func TestUpdateDashboard_FullBodyResultHasNoAppliedOperations(t *testing.T) {
	result, err := updateDashboard(mockCtxWithClient(appliedEchoServer(t)), UpdateDashboardParams{
		Dashboard: map[string]interface{}{"uid": "dash-1", "title": "Whole body", "version": float64(3), "panels": []interface{}{}},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Applied)

	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	assert.NotContains(t, fields, "applied")
	assert.JSONEq(t, `"dash-1"`, string(fields["uid"]))
}
