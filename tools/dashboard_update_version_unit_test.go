//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

// savedDashboardCommand is the body of a POST /api/dashboards/db as the fake
// Grafana received it.
type savedDashboardCommand struct {
	Dashboard map[string]interface{} `json:"dashboard"`
	FolderUID string                 `json:"folderUid"`
	Message   string                 `json:"message"`
	Overwrite bool                   `json:"overwrite"`
}

// legacySaveRecorder records legacy dashboard saves and answers them with a
// configurable status, so a test can play Grafana accepting or rejecting a
// version-checked save.
type legacySaveRecorder struct {
	mu         sync.Mutex
	saves      []savedDashboardCommand
	saveStatus int
	saveBody   string
}

func (rec *legacySaveRecorder) handleSave(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || r.URL.Path != "/api/dashboards/db" {
		return false
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var cmd savedDashboardCommand
	assert.NoError(t, json.NewDecoder(r.Body).Decode(&cmd))
	rec.saves = append(rec.saves, cmd)
	w.Header().Set("Content-Type", "application/json")
	if rec.saveStatus != 0 && rec.saveStatus != http.StatusOK {
		w.WriteHeader(rec.saveStatus)
		_, _ = w.Write([]byte(rec.saveBody))
		return true
	}
	_, _ = w.Write([]byte(`{"id":7,"uid":"dash-1","status":"success","version":4,"url":"/d/dash-1/dash"}`))
	return true
}

func (rec *legacySaveRecorder) only(t *testing.T) savedDashboardCommand {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.saves, 1, "expected exactly one save")
	return rec.saves[0]
}

// legacyDashboardContext serves a classic dashboard at version 3 through the
// legacy API (Grafana without the Kubernetes dashboard API) and records saves.
func legacyDashboardContext(t *testing.T, rec *legacySaveRecorder) context.Context {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rec.handleSave(t, w, r) {
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/dashboards/uid/dash-1" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"dashboard": map[string]interface{}{
					"id": 7, "uid": "dash-1", "title": "Before", "version": 3,
					"panels": []interface{}{map[string]interface{}{"id": 1, "title": "CPU"}},
				},
				"meta": map[string]interface{}{"folderUid": "folder-1"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	return mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.NewGrafanaClient(ctx, ts.URL, "test-api-key", nil))
}

var retitle = []PatchOperation{{Op: "replace", Path: "$.title", Value: "After"}}

func TestUpdateDashboardPatch_SaveIsVersionChecked(t *testing.T) {
	t.Run("sends the fetched version and does not overwrite", func(t *testing.T) {
		rec := &legacySaveRecorder{}
		ctx := legacyDashboardContext(t, rec)

		_, err := updateDashboard(ctx, UpdateDashboardParams{UID: "dash-1", Operations: retitle, Message: "rename"})
		require.NoError(t, err)

		saved := rec.only(t)
		assert.False(t, saved.Overwrite, "a patch must not force overwrite, or Grafana skips the version check")
		assert.Equal(t, float64(3), saved.Dashboard["version"], "the save must carry the version that was read")
		assert.Equal(t, "After", saved.Dashboard["title"])
		assert.Equal(t, "dash-1", saved.Dashboard["uid"])
		assert.Equal(t, float64(7), saved.Dashboard["id"])
		assert.Equal(t, "folder-1", saved.FolderUID)
		assert.Equal(t, "rename", saved.Message)
	})

	t.Run("the caller's overwrite=true is passed through", func(t *testing.T) {
		rec := &legacySaveRecorder{}
		ctx := legacyDashboardContext(t, rec)

		_, err := updateDashboard(ctx, UpdateDashboardParams{UID: "dash-1", Operations: retitle, Overwrite: true})
		require.NoError(t, err)

		saved := rec.only(t)
		assert.True(t, saved.Overwrite)
		assert.Equal(t, "After", saved.Dashboard["title"])
	})

	t.Run("a 412 for a stale version is returned to the caller", func(t *testing.T) {
		rec := &legacySaveRecorder{
			saveStatus: http.StatusPreconditionFailed,
			saveBody:   `{"message":"The dashboard has been changed by someone else","status":"version-mismatch"}`,
		}
		ctx := legacyDashboardContext(t, rec)

		result, err := updateDashboard(ctx, UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unable to save dashboard")
		assert.Contains(t, err.Error(), "412")
		assert.Contains(t, err.Error(), "The dashboard has been changed by someone else")
		assert.False(t, rec.only(t).Overwrite, "the rejected save must not have been retried with overwrite")
	})

	t.Run("a 409 for a stale version is returned to the caller", func(t *testing.T) {
		rec := &legacySaveRecorder{
			saveStatus: http.StatusConflict,
			saveBody:   `{"message":"the dashboard has been changed by someone else"}`,
		}
		ctx := legacyDashboardContext(t, rec)

		result, err := updateDashboard(ctx, UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "status 409")
		assert.Contains(t, err.Error(), "changed since it was read")
		assert.Contains(t, err.Error(), "get_dashboard_by_uid")
		assert.False(t, rec.only(t).Overwrite, "the rejected save must not have been retried with overwrite")
	})
}

// On a Grafana that serves dashboards through the Kubernetes API, a classic
// dashboard is read from there and saved through the legacy endpoint, whose
// version is the object's metadata.generation.
func TestUpdateDashboardPatch_K8sReadSavesWithGenerationAsVersion(t *testing.T) {
	newContext := func(t *testing.T, rec *legacySaveRecorder) context.Context {
		t.Helper()
		ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
			if serveDashboardDiscovery(w, r, "v1beta1") {
				return
			}
			if rec.handleSave(t, w, r) {
				return
			}
			if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/v1beta1/") && strings.HasSuffix(r.URL.Path, "/dashboards/dash-1") {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"apiVersion": "dashboard.grafana.app/v1beta1",
					"kind":       "Dashboard",
					"metadata": map[string]interface{}{
						"name": "dash-1", "generation": 7, "resourceVersion": "1731000000000",
						"annotations": map[string]interface{}{"grafana.app/folder": "folder-1"},
					},
					// The stored spec carries its own, unrelated version number.
					"spec": map[string]interface{}{"title": "Before", "version": 99, "panels": []interface{}{}},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		t.Cleanup(ts.Close)
		return staleCtx(ts)
	}

	t.Run("generation is sent as the version, without overwrite", func(t *testing.T) {
		rec := &legacySaveRecorder{}
		_, err := updateDashboard(newContext(t, rec), UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.NoError(t, err)

		saved := rec.only(t)
		assert.False(t, saved.Overwrite)
		assert.Equal(t, float64(7), saved.Dashboard["version"], "metadata.generation, not the spec's own version")
		assert.Equal(t, "After", saved.Dashboard["title"])
		assert.Equal(t, "dash-1", saved.Dashboard["uid"])
		assert.Equal(t, "folder-1", saved.FolderUID)
	})

	t.Run("a patch cannot change the version used for the check", func(t *testing.T) {
		rec := &legacySaveRecorder{}
		_, err := updateDashboard(newContext(t, rec), UpdateDashboardParams{
			UID: "dash-1",
			Operations: []PatchOperation{
				{Op: "replace", Path: "$.version", Value: float64(1)},
				{Op: "replace", Path: "$.title", Value: "After"},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, float64(7), rec.only(t).Dashboard["version"])
	})

	t.Run("a conflict on the version-checked save is returned to the caller", func(t *testing.T) {
		rec := &legacySaveRecorder{saveStatus: http.StatusConflict, saveBody: `{}`}
		_, err := updateDashboard(newContext(t, rec), UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 409")
		assert.Contains(t, err.Error(), "changed since it was read")
		assert.Equal(t, float64(7), rec.only(t).Dashboard["version"])
	})
}

// expectedVersion guards a full-body overwrite of a v2 dashboard, which
// otherwise replaces whatever is stored.
func TestCreateOrUpdateDashboardV2_ExpectedVersion(t *testing.T) {
	newContext := func(t *testing.T, exists bool, writes *[]string) context.Context {
		t.Helper()
		ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
			if serveDashboardDiscovery(w, r, "v1beta1", "v2beta1") {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method != http.MethodGet:
				*writes = append(*writes, r.Method+" "+r.URL.Path)
				var body map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["metadata"] == nil {
					body["metadata"] = map[string]interface{}{"name": "dash-v2"}
				}
				_ = json.NewEncoder(w).Encode(body)
			case !exists:
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"kind": "Status", "code": 404, "reason": "NotFound"})
			case strings.Contains(r.URL.Path, "/v1beta1/"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"apiVersion": "dashboard.grafana.app/v1beta1",
					"metadata":   map[string]interface{}{"name": "dash-v2", "generation": 5},
					"spec":       map[string]interface{}{"title": "Down-converted", "panels": []interface{}{}},
					"status":     map[string]interface{}{"conversion": map[string]interface{}{"storedVersion": "v2beta1"}},
				})
			case strings.Contains(r.URL.Path, "/v2beta1/"):
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"apiVersion": "dashboard.grafana.app/v2beta1",
					"metadata":   map[string]interface{}{"name": "dash-v2", "generation": 5, "resourceVersion": "42"},
					"spec":       map[string]interface{}{"title": "Stored", "elements": map[string]interface{}{}},
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(ts.Close)

		k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
		ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
		return mcpgrafana.WithKubernetesClient(ctx, k8s)
	}
	body := func() map[string]interface{} {
		return map[string]interface{}{"uid": "dash-v2", "title": "Mine", "elements": map[string]interface{}{}}
	}
	version := func(v int64) *int64 { return &v }

	t.Run("stale expectedVersion is a conflict and nothing is written", func(t *testing.T) {
		var writes []string
		_, err := createOrUpdateDashboardV2(newContext(t, true, &writes), UpdateDashboardParams{
			Dashboard: body(), Overwrite: true, ExpectedVersion: version(4),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has changed since version 4 (current version is 5)")
		assert.Contains(t, err.Error(), "get_dashboard_by_uid")
		assert.Empty(t, writes, "a stale overwrite must not reach the server")
	})

	t.Run("a newer expectedVersion than the stored one is also a conflict", func(t *testing.T) {
		var writes []string
		_, err := createOrUpdateDashboardV2(newContext(t, true, &writes), UpdateDashboardParams{
			Dashboard: body(), Overwrite: true, ExpectedVersion: version(6),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "current version is 5")
		assert.Empty(t, writes)
	})

	t.Run("matching expectedVersion overwrites", func(t *testing.T) {
		var writes []string
		_, err := createOrUpdateDashboardV2(newContext(t, true, &writes), UpdateDashboardParams{
			Dashboard: body(), Overwrite: true, ExpectedVersion: version(5),
		})
		require.NoError(t, err)
		require.Len(t, writes, 1)
		assert.True(t, strings.HasPrefix(writes[0], http.MethodPut+" "), writes[0])
		assert.Contains(t, writes[0], "/v2beta1/")
	})

	t.Run("omitting expectedVersion keeps the unconditional overwrite", func(t *testing.T) {
		var writes []string
		_, err := createOrUpdateDashboardV2(newContext(t, true, &writes), UpdateDashboardParams{
			Dashboard: body(), Overwrite: true,
		})
		require.NoError(t, err)
		require.Len(t, writes, 1)
		assert.True(t, strings.HasPrefix(writes[0], http.MethodPut+" "), writes[0])
	})

	t.Run("overwrite=false still refuses before expectedVersion is considered", func(t *testing.T) {
		var writes []string
		_, err := createOrUpdateDashboardV2(newContext(t, true, &writes), UpdateDashboardParams{
			Dashboard: body(), ExpectedVersion: version(5),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		assert.Empty(t, writes)
	})

	t.Run("expectedVersion does not block creating a dashboard that does not exist", func(t *testing.T) {
		var writes []string
		_, err := createOrUpdateDashboardV2(newContext(t, false, &writes), UpdateDashboardParams{
			Dashboard: body(), Overwrite: true, ExpectedVersion: version(4),
		})
		require.NoError(t, err)
		require.Len(t, writes, 1)
		assert.True(t, strings.HasPrefix(writes[0], http.MethodPost+" "), writes[0])
	})

	t.Run("update_dashboard passes expectedVersion through", func(t *testing.T) {
		var writes []string
		_, err := updateDashboard(newContext(t, true, &writes), UpdateDashboardParams{
			Dashboard: body(), Overwrite: true, ExpectedVersion: version(4),
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has changed since version 4 (current version is 5)")
		assert.Empty(t, writes)
	})
}
