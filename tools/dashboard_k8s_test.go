package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

func TestFetchDashboardViaK8s_V1SingleFetch(t *testing.T) {
	var v1Calls, v2Calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v1beta1/"):
			atomic.AddInt32(&v1Calls, 1)
			w.Header().Set("Content-Type", "application/json")
			// Classic dashboard stored as v1: no status.conversion.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "dashboard.grafana.app/v1beta1",
				"kind":       "Dashboard",
				"metadata":   map[string]interface{}{"name": "abc", "annotations": map[string]interface{}{"grafana.app/folder": "f1"}},
				"spec":       map[string]interface{}{"title": "Classic", "panels": []interface{}{}},
			})
		case strings.Contains(r.URL.Path, "/v2beta1/"):
			atomic.AddInt32(&v2Calls, 1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	res, err := fetchDashboardViaK8s(ctx, k8s, "abc")
	require.NoError(t, err)
	assert.False(t, res.IsV2)
	assert.Equal(t, "v1beta1", res.APIVersion)
	assert.Equal(t, "Classic", safeString(res.Spec, "title"))
	assert.Equal(t, "f1", res.Meta.FolderUID)
	assert.Equal(t, "abc", safeString(res.Spec, "uid"), "uid should be injected into v1 body")
	assert.Equal(t, int32(1), atomic.LoadInt32(&v1Calls))
	assert.Equal(t, int32(0), atomic.LoadInt32(&v2Calls), "v1 dashboards must not trigger a second fetch")
}

func TestFetchDashboardViaK8s_V2Refetch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/v1beta1/"):
			// v2-stored dashboard fetched at v1beta1: lossy conversion with a
			// status.conversion.storedVersion pointing at the native version.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "dashboard.grafana.app/v1beta1",
				"kind":       "Dashboard",
				"metadata":   map[string]interface{}{"name": "v2-test-uid"},
				"spec":       map[string]interface{}{"title": "Down-converted", "panels": []interface{}{}},
				"status": map[string]interface{}{
					"conversion": map[string]interface{}{"failed": true, "storedVersion": "v2beta1"},
				},
			})
		case strings.Contains(r.URL.Path, "/v2beta1/"):
			http.ServeFile(w, r, "testdata/v2beta1_dashboard.json")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})

	res, err := fetchDashboardViaK8s(ctx, k8s, "v2-test-uid")
	require.NoError(t, err)
	assert.True(t, res.IsV2)
	assert.Equal(t, "v2beta1", res.APIVersion)
	assert.Equal(t, "V2 Test Dashboard", safeString(res.Spec, "title"))
	assert.Contains(t, res.Spec, "elements", "native v2 spec should carry elements")
	assert.NotNil(t, res.Object, "k8s object should be retained for writes")
}

// TestFetchDashboardCapabilityGate verifies that fetchDashboard only uses the
// k8s path when the dashboard.grafana.app group serves v1beta1 (Grafana 12+).
// On a group that omits v1beta1 (older Grafana) or is absent, SupportsGroupVersion
// is false, so fetchDashboard would fall back to the legacy API.
func TestFetchDashboardCapabilityGate(t *testing.T) {
	t.Run("group with v1beta1 is used", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/apis/"+dashboardAPIGroup {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"versions": []map[string]interface{}{{"version": "v0alpha1"}, {"version": "v1beta1"}, {"version": "v2beta1"}},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()
		k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
		assert.True(t, k8s.SupportsGroupVersion(context.Background(), dashboardAPIGroup, dashboardReadVersion))
	})

	t.Run("group without v1beta1 (older Grafana) is not used", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/apis/"+dashboardAPIGroup {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"versions": []map[string]interface{}{{"version": "v0alpha1"}, {"version": "v1alpha1"}, {"version": "v2alpha1"}},
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()
		k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
		assert.False(t, k8s.SupportsGroupVersion(context.Background(), dashboardAPIGroup, dashboardReadVersion))
	})

	t.Run("absent group (pre-app-platform) is not used", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()
		k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
		assert.False(t, k8s.SupportsGroupVersion(context.Background(), dashboardAPIGroup, dashboardReadVersion))
	})
}

// TestFetchDashboard_FailsClosedOnTransientDiscoveryError verifies that an
// inconclusive capability check (discovery errors with a non-404) does NOT fall
// back to the legacy API — which on a v2-capable Grafana would be lossy and, for
// writes, corrupting. It must surface the error instead.
func TestFetchDashboard_FailsClosedOnTransientDiscoveryError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Transient failure on the discovery endpoint (not a definitive 404).
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	ctx = mcpgrafana.WithKubernetesClient(ctx, k8s)

	_, err := fetchDashboard(ctx, "some-uid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capability", "should fail closed on inconclusive discovery, not use lossy legacy")
}

// TestUpdateDashboardV2_SetsFolderAndMessageAnnotations verifies a v2 update
// honors folderUid and message by writing the grafana.app/folder and
// grafana.app/message metadata annotations.
func TestUpdateDashboardV2_SetsFolderAndMessageAnnotations(t *testing.T) {
	var putBody map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_ = json.NewDecoder(r.Body).Decode(&putBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"metadata": map[string]interface{}{"name": "u1", "generation": 2},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	ctx = mcpgrafana.WithKubernetesClient(ctx, k8s)

	res := &dashboardResult{
		Spec:       map[string]interface{}{"title": "t"},
		APIVersion: "v2beta1",
		IsV2:       true,
		Object: map[string]interface{}{
			"apiVersion": "dashboard.grafana.app/v2beta1",
			"kind":       "Dashboard",
			"metadata":   map[string]interface{}{"name": "u1", "resourceVersion": "5"},
			"spec":       map[string]interface{}{"title": "t"},
		},
	}

	_, err := updateDashboardV2(ctx, "u1", res, UpdateDashboardParams{FolderUID: "folder-xyz", Message: "tweak title"})
	require.NoError(t, err)

	md, ok := putBody["metadata"].(map[string]interface{})
	require.True(t, ok)
	ann, ok := md["annotations"].(map[string]interface{})
	require.True(t, ok, "annotations should be written")
	assert.Equal(t, "folder-xyz", ann["grafana.app/folder"])
	assert.Equal(t, "tweak title", ann["grafana.app/message"])
}

// TestCreateOrUpdateDashboardV2_RespectsOverwriteFalse verifies a full-JSON v2
// save refuses to replace an existing dashboard when overwrite is false, matching
// the legacy save path.
func TestCreateOrUpdateDashboardV2_RespectsOverwriteFalse(t *testing.T) {
	var wrote bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dashboards/u1"):
			// The dashboard already exists.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "dashboard.grafana.app/v2beta1",
				"kind":       "Dashboard",
				"metadata":   map[string]interface{}{"name": "u1", "resourceVersion": "3"},
				"spec":       map[string]interface{}{"title": "old"},
			})
		case r.Method == http.MethodPut || r.Method == http.MethodPost:
			wrote = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	ctx = mcpgrafana.WithKubernetesClient(ctx, k8s)

	_, err := createOrUpdateDashboardV2(ctx, UpdateDashboardParams{
		Dashboard: map[string]interface{}{"uid": "u1", "title": "new", "elements": map[string]interface{}{}},
		Overwrite: false,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.False(t, wrote, "must not write when overwrite is false and the dashboard exists")
}

// TestCreateOrUpdateDashboardV2_RejectsV2BodyOverV1Stored verifies that replacing
// a dashboard currently stored as classic v1 with a v2 full-JSON body is rejected
// (Grafana pins the stored schema version, so it would be silently down-converted)
// and that no write is attempted.
func TestCreateOrUpdateDashboardV2_RejectsV2BodyOverV1Stored(t *testing.T) {
	var wrote bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dashboards/u1"):
			// Existing dashboard is stored as classic v1 (no v2 conversion status).
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "dashboard.grafana.app/v1beta1",
				"kind":       "Dashboard",
				"metadata":   map[string]interface{}{"name": "u1", "resourceVersion": "7"},
				"spec":       map[string]interface{}{"title": "old", "panels": []interface{}{}},
			})
		case r.Method == http.MethodPut || r.Method == http.MethodPost:
			wrote = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	ctx = mcpgrafana.WithKubernetesClient(ctx, k8s)

	_, err := createOrUpdateDashboardV2(ctx, UpdateDashboardParams{
		Dashboard: map[string]interface{}{"uid": "u1", "title": "new", "elements": map[string]interface{}{}},
		Overwrite: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stored as classic v1")
	assert.False(t, wrote, "must not write a v2 body over a v1-stored dashboard")
}

// TestCreateOrUpdateDashboardV2_DoesNotMutateInput verifies the caller's dashboard
// map is not mutated (its uid survives) when saving a v2 dashboard.
func TestCreateOrUpdateDashboardV2_DoesNotMutateInput(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"metadata": map[string]interface{}{"name": "newdash", "generation": 1},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound) // GET existing -> not found -> create
	}))
	defer ts.Close()

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	ctx = mcpgrafana.WithKubernetesClient(ctx, k8s)

	input := map[string]interface{}{"uid": "newdash", "title": "t", "elements": map[string]interface{}{}}
	_, err := createOrUpdateDashboardV2(ctx, UpdateDashboardParams{Dashboard: input, Overwrite: true})
	require.NoError(t, err)
	assert.Equal(t, "newdash", input["uid"], "caller's dashboard map must not be mutated")
}
