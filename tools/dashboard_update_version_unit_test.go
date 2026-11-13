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
		assert.Contains(t, err.Error(), "changed since it was read", "a 412 carries the same recovery hint as a 409")
		assert.Contains(t, err.Error(), "send the same patch again")
		assert.False(t, rec.only(t).Overwrite, "the rejected save must not have been retried with overwrite")
	})

	t.Run("a patch that replaces or removes version still sends the version that was read", func(t *testing.T) {
		for _, op := range []PatchOperation{
			{Op: "replace", Path: "$.version", Value: 99},
			{Op: "remove", Path: "$.version"},
		} {
			rec := &legacySaveRecorder{}
			ctx := legacyDashboardContext(t, rec)

			_, err := updateDashboard(ctx, UpdateDashboardParams{UID: "dash-1", Operations: append([]PatchOperation{op}, retitle...)})
			require.NoError(t, err, op.Op)

			saved := rec.only(t)
			assert.Equal(t, float64(3), saved.Dashboard["version"], "%s of $.version must not change the version the save is checked against", op.Op)
			assert.False(t, saved.Overwrite)
			assert.Equal(t, "After", saved.Dashboard["title"])
		}
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

// k8sClassicDashboard is a fake Grafana that serves one classic dashboard
// through the Kubernetes API (generation 7) and records how it is saved:
// updates of the Kubernetes object in puts, legacy saves in legacy.
type k8sClassicDashboard struct {
	legacy          legacySaveRecorder
	resourceVersion string
	putStatus       int

	mu   sync.Mutex
	puts []map[string]interface{}
	path string
}

const k8sClassicDashboardPath = "/apis/dashboard.grafana.app/v1beta1/namespaces/default/dashboards/dash-1"

func (f *k8sClassicDashboard) context(t *testing.T) context.Context {
	t.Helper()
	ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
		if serveDashboardDiscovery(w, r, "v1beta1") {
			return
		}
		if f.legacy.handleSave(t, w, r) {
			return
		}
		if r.URL.Path != k8sClassicDashboardPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			metadata := map[string]interface{}{
				"name": "dash-1", "namespace": "default", "generation": 7,
				"labels": map[string]interface{}{"grafana.app/deprecatedInternalID": "42"},
				"annotations": map[string]interface{}{
					"grafana.app/folder":    "folder-1",
					"grafana.app/message":   "note of the previous save",
					"grafana.app/createdBy": "user:u1",
				},
			}
			if f.resourceVersion != "" {
				metadata["resourceVersion"] = f.resourceVersion
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "dashboard.grafana.app/v1beta1",
				"kind":       "Dashboard",
				"metadata":   metadata,
				// The stored spec carries its own, unrelated version number.
				"spec": map[string]interface{}{
					"title": "Before", "version": 99, "schemaVersion": 41,
					"panels": []interface{}{map[string]interface{}{"id": 1, "title": "CPU"}},
				},
			})
		case http.MethodPut:
			var body map[string]interface{}
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			f.mu.Lock()
			f.puts = append(f.puts, body)
			f.path = r.URL.Path
			f.mu.Unlock()
			if f.putStatus != 0 && f.putStatus != http.StatusOK {
				w.WriteHeader(f.putStatus)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"kind": "Status", "status": "Failure", "code": f.putStatus,
					"message": "the object has been modified; please apply your changes to the latest version and try again",
				})
				return
			}
			if metadata, ok := body["metadata"].(map[string]interface{}); ok {
				metadata["generation"] = 8
			}
			_ = json.NewEncoder(w).Encode(body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(ts.Close)
	return staleCtx(ts)
}

func (f *k8sClassicDashboard) onlyPut(t *testing.T) (metadata, annotations, spec map[string]interface{}) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.puts, 1, "expected exactly one update through the k8s API")
	metadata, _ = f.puts[0]["metadata"].(map[string]interface{})
	require.NotNil(t, metadata)
	annotations, _ = metadata["annotations"].(map[string]interface{})
	require.NotNil(t, annotations)
	spec, _ = f.puts[0]["spec"].(map[string]interface{})
	require.NotNil(t, spec)
	return metadata, annotations, spec
}

// On a Grafana that serves dashboards through the Kubernetes API, a patch to a
// classic dashboard is saved as an update of the object that was read, carrying
// its resourceVersion, so the server rejects it if the dashboard changed.
func TestUpdateDashboardPatch_K8sReadSavesThroughK8sWithResourceVersion(t *testing.T) {
	t.Run("the object goes back with the resourceVersion that was read", func(t *testing.T) {
		fake := &k8sClassicDashboard{resourceVersion: "1731000000000"}
		result, err := updateDashboard(fake.context(t), UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.NoError(t, err)

		assert.Empty(t, fake.legacy.saves, "the legacy save endpoint must not be used")
		metadata, annotations, spec := fake.onlyPut(t)
		assert.Equal(t, k8sClassicDashboardPath, fake.path)
		assert.Equal(t, "1731000000000", metadata["resourceVersion"])
		assert.Equal(t, "dash-1", metadata["name"])
		assert.Equal(t, map[string]interface{}{"grafana.app/deprecatedInternalID": "42"}, metadata["labels"], "labels go back as read")
		assert.Equal(t, "folder-1", annotations["grafana.app/folder"], "the dashboard must stay in its folder")
		assert.Equal(t, "user:u1", annotations["grafana.app/createdBy"], "other annotations go back as read")
		assert.NotContains(t, annotations, "grafana.app/message", "the previous save's note must not label this version")

		assert.Equal(t, "After", spec["title"])
		assert.Equal(t, float64(41), spec["schemaVersion"])
		assert.Equal(t, []interface{}{map[string]interface{}{"id": float64(1), "title": "CPU"}}, spec["panels"])
		assert.NotContains(t, spec, "uid", "identity is metadata.name, not a spec field")
		assert.NotContains(t, spec, "version", "the version is metadata.generation, not a spec field")

		require.NotNil(t, result)
		assert.Equal(t, "dash-1", *result.UID)
		assert.Equal(t, "After", *result.Title)
		assert.Equal(t, int64(8), *result.Version)
		assert.Equal(t, "folder-1", result.FolderUID)
		assert.Equal(t, "success", *result.Status)
	})

	t.Run("the caller's message and folder are applied", func(t *testing.T) {
		fake := &k8sClassicDashboard{resourceVersion: "1731000000000"}
		_, err := updateDashboard(fake.context(t), UpdateDashboardParams{
			UID: "dash-1", Operations: retitle, Message: "rename", FolderUID: "folder-2",
		})
		require.NoError(t, err)

		_, annotations, _ := fake.onlyPut(t)
		assert.Equal(t, "rename", annotations["grafana.app/message"])
		assert.Equal(t, "folder-2", annotations["grafana.app/folder"])
	})

	t.Run("a 409 is returned to the caller as a conflict", func(t *testing.T) {
		fake := &k8sClassicDashboard{resourceVersion: "1731000000000", putStatus: http.StatusConflict}
		result, err := updateDashboard(fake.context(t), UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unable to save dashboard")
		assert.Contains(t, err.Error(), "changed since it was read")
		assert.Contains(t, err.Error(), "get_dashboard_by_uid")
		assert.Contains(t, err.Error(), "HTTP 409")

		fake.onlyPut(t)
		assert.Empty(t, fake.legacy.saves, "a rejected save must not be retried through the legacy endpoint")
	})

	t.Run("another failure is returned as it is", func(t *testing.T) {
		fake := &k8sClassicDashboard{resourceVersion: "1731000000000", putStatus: http.StatusInternalServerError}
		_, err := updateDashboard(fake.context(t), UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `update dashboard "dash-1" via k8s api`)
		assert.NotContains(t, err.Error(), "changed since it was read")
		assert.Empty(t, fake.legacy.saves, "a failed save must not be retried through the legacy endpoint")
	})

	t.Run("overwrite=true keeps the legacy overwrite", func(t *testing.T) {
		fake := &k8sClassicDashboard{resourceVersion: "1731000000000"}
		_, err := updateDashboard(fake.context(t), UpdateDashboardParams{UID: "dash-1", Operations: retitle, Overwrite: true})
		require.NoError(t, err)

		assert.Empty(t, fake.puts)
		saved := fake.legacy.only(t)
		assert.True(t, saved.Overwrite)
		assert.Equal(t, "After", saved.Dashboard["title"])
		assert.Equal(t, "dash-1", saved.Dashboard["uid"])
		assert.Equal(t, "folder-1", saved.FolderUID)
	})

	t.Run("without a resourceVersion the version-checked legacy save is used", func(t *testing.T) {
		fake := &k8sClassicDashboard{}
		_, err := updateDashboard(fake.context(t), UpdateDashboardParams{
			UID: "dash-1",
			Operations: []PatchOperation{
				{Op: "replace", Path: "$.version", Value: float64(1)},
				{Op: "replace", Path: "$.title", Value: "After"},
			},
		})
		require.NoError(t, err)

		assert.Empty(t, fake.puts)
		saved := fake.legacy.only(t)
		assert.False(t, saved.Overwrite)
		assert.Equal(t, float64(7), saved.Dashboard["version"], "metadata.generation, whatever the spec or a patch says")
		assert.Equal(t, "After", saved.Dashboard["title"])
	})

	t.Run("a conflict on that legacy save is returned to the caller", func(t *testing.T) {
		fake := &k8sClassicDashboard{}
		fake.legacy.saveStatus = http.StatusConflict
		fake.legacy.saveBody = `{}`
		_, err := updateDashboard(fake.context(t), UpdateDashboardParams{UID: "dash-1", Operations: retitle})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status 409")
		assert.Contains(t, err.Error(), "changed since it was read")
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

// expectedVersion on a classic full body uses Grafana's own check: the save
// carries the expected version and no overwrite, so Grafana rejects it when the
// stored version differs.
func TestUpdateDashboardFullBody_ExpectedVersionOnClassicBody(t *testing.T) {
	version := func(v int64) *int64 { return &v }
	body := func() map[string]interface{} {
		return map[string]interface{}{"uid": "dash-1", "title": "Mine", "version": float64(99), "panels": []interface{}{}}
	}

	t.Run("overwrite=true with expectedVersion becomes a version-checked save", func(t *testing.T) {
		rec := &legacySaveRecorder{}
		input := body()
		_, err := updateDashboard(legacyDashboardContext(t, rec), UpdateDashboardParams{
			Dashboard: input, Overwrite: true, ExpectedVersion: version(3),
		})
		require.NoError(t, err)

		saved := rec.only(t)
		assert.False(t, saved.Overwrite, "overwrite would make Grafana skip the version check")
		assert.Equal(t, float64(3), saved.Dashboard["version"], "the expected version, not the one in the body")
		assert.Equal(t, "Mine", saved.Dashboard["title"])
		assert.Equal(t, float64(99), input["version"], "the caller's body must not be modified")
	})

	t.Run("a body without a version gets the expected one", func(t *testing.T) {
		rec := &legacySaveRecorder{}
		input := body()
		delete(input, "version")
		_, err := updateDashboard(legacyDashboardContext(t, rec), UpdateDashboardParams{
			Dashboard: input, ExpectedVersion: version(3),
		})
		require.NoError(t, err)

		saved := rec.only(t)
		assert.False(t, saved.Overwrite)
		assert.Equal(t, float64(3), saved.Dashboard["version"])
		assert.NotContains(t, input, "version", "the caller's body must not be modified")
	})

	t.Run("a stale expectedVersion is rejected by Grafana and returned to the caller", func(t *testing.T) {
		for _, tc := range []struct {
			status int
			body   string
			want   string
		}{
			{http.StatusPreconditionFailed, `{"message":"The dashboard has been changed by someone else","status":"version-mismatch"}`, "The dashboard has been changed by someone else"},
			{http.StatusConflict, `{}`, "status 409"},
		} {
			rec := &legacySaveRecorder{saveStatus: tc.status, saveBody: tc.body}
			result, err := updateDashboard(legacyDashboardContext(t, rec), UpdateDashboardParams{
				Dashboard: body(), Overwrite: true, ExpectedVersion: version(2),
			})
			require.Error(t, err)
			assert.Nil(t, result)
			assert.Contains(t, err.Error(), tc.want)
			saved := rec.only(t)
			assert.False(t, saved.Overwrite, "the rejected save must not have been retried with overwrite")
			assert.Equal(t, float64(2), saved.Dashboard["version"])
		}
	})

	t.Run("without expectedVersion overwrite=true overwrites as before", func(t *testing.T) {
		rec := &legacySaveRecorder{}
		_, err := updateDashboard(legacyDashboardContext(t, rec), UpdateDashboardParams{Dashboard: body(), Overwrite: true})
		require.NoError(t, err)

		saved := rec.only(t)
		assert.True(t, saved.Overwrite)
		assert.Equal(t, float64(99), saved.Dashboard["version"], "the body is sent as given")
	})
}
