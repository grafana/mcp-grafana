package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const testFilesPrefix = "/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/repo-1/files/"

type gitSyncRequest struct {
	Method string
	Path   string
	Query  map[string]string
	Body   map[string]interface{}
}

// newGitSyncServer serves dashboard discovery, the given dashboards and
// folders by name, and records every files API call and dashboard write.
func newGitSyncServer(t *testing.T, dashboards, folders map[string]map[string]interface{}) (context.Context, *[]gitSyncRequest) {
	t.Helper()
	var calls []gitSyncRequest
	ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case p == "/apis/"+dashboardAPIGroup:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"versions": []map[string]interface{}{{"version": "v0alpha1"}, {"version": "v1beta1"}},
			})
		case strings.HasPrefix(p, testFilesPrefix):
			call := gitSyncRequest{Method: r.Method, Path: strings.TrimPrefix(p, testFilesPrefix), Query: map[string]string{}}
			for k := range r.URL.Query() {
				call.Query[k] = r.URL.Query().Get(k)
			}
			_ = json.NewDecoder(r.Body).Decode(&call.Body)
			calls = append(calls, call)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"kind": "ResourceWrapper"})
		case r.Method == http.MethodGet && strings.HasPrefix(p, "/apis/dashboard.grafana.app/v1beta1/namespaces/default/dashboards/"):
			if obj, ok := dashboards[p[strings.LastIndex(p, "/")+1:]]; ok {
				_ = json.NewEncoder(w).Encode(obj)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && strings.HasPrefix(p, "/apis/folder.grafana.app/v1beta1/namespaces/default/folders/"):
			if obj, ok := folders[p[strings.LastIndex(p, "/")+1:]]; ok {
				_ = json.NewEncoder(w).Encode(obj)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			calls = append(calls, gitSyncRequest{Method: r.Method, Path: p})
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(ts.Close)

	k8s := &mcpgrafana.KubernetesClient{BaseURL: ts.URL, HTTPClient: ts.Client()}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	return mcpgrafana.WithKubernetesClient(ctx, k8s), &calls
}

func k8sObject(kind, name string, annotations map[string]interface{}, spec map[string]interface{}) map[string]interface{} {
	group := dashboardAPIGroup
	if kind == "Folder" {
		group = folderAPIGroup
	}
	return map[string]interface{}{
		"apiVersion": group + "/v1beta1",
		"kind":       kind,
		"metadata":   map[string]interface{}{"name": name, "annotations": annotations},
		"spec":       spec,
	}
}

func repoManaged(sourcePath string) map[string]interface{} {
	return map[string]interface{}{
		"grafana.app/folder":     "f-synced",
		"grafana.app/managedBy":  "repo",
		"grafana.app/managerId":  "repo-1",
		"grafana.app/sourcePath": sourcePath,
	}
}

func TestUpdateDashboard_PatchOnRepoManagedDashboardCommitsFile(t *testing.T) {
	for _, ref := range []string{"", "mcp/edit"} {
		t.Run("ref="+ref, func(t *testing.T) {
			ctx, calls := newGitSyncServer(t, map[string]map[string]interface{}{
				"d1": k8sObject("Dashboard", "d1", repoManaged("team/d1.json"), map[string]interface{}{"title": "Old", "id": 7, "version": 3, "panels": []interface{}{}}),
			}, nil)

			res, err := updateDashboardContent(ctx, UpdateDashboardParams{
				UID:        "d1",
				Operations: []PatchOperation{{Op: "replace", Path: "$.title", Value: "New"}},
				Message:    "rename",
				Ref:        ref,
			})
			require.NoError(t, err)

			require.Len(t, *calls, 1, "only the files API is written to")
			call := (*calls)[0]
			assert.Equal(t, http.MethodPut, call.Method)
			assert.Equal(t, "team/d1.json", call.Path)
			assert.Equal(t, "rename", call.Query["message"])
			assert.Equal(t, ref, call.Query["ref"])
			assert.Equal(t, "dashboard.grafana.app/v1beta1", call.Body["apiVersion"])
			assert.Equal(t, map[string]interface{}{"name": "d1"}, call.Body["metadata"])
			spec := call.Body["spec"].(map[string]interface{})
			assert.Equal(t, "New", spec["title"])
			assert.NotContains(t, spec, "id")
			assert.NotContains(t, spec, "version")
			assert.Equal(t, "d1", *res.UID)
			if ref != "" {
				assert.Contains(t, *res.Status, "merged")
			}
		})
	}
}

func TestUpdateDashboard_FullJSONIntoRepoManagedFolder(t *testing.T) {
	folders := map[string]map[string]interface{}{
		"f-synced": k8sObject("Folder", "f-synced", repoManaged("team"), map[string]interface{}{"title": "team"}),
	}
	unmanaged := map[string]interface{}{"grafana.app/folder": "f-mine"}

	t.Run("new dashboard is created as a file in the folder", func(t *testing.T) {
		ctx, calls := newGitSyncServer(t, nil, folders)
		_, err := updateDashboardContent(ctx, UpdateDashboardParams{
			Dashboard: map[string]interface{}{"uid": "new-1", "title": "New", "panels": []interface{}{}},
			FolderUID: "f-synced",
		})
		require.NoError(t, err)
		require.Len(t, *calls, 1)
		assert.Equal(t, http.MethodPost, (*calls)[0].Method)
		assert.Equal(t, "team/new-1.json", (*calls)[0].Path)
		assert.Equal(t, `Create dashboard "New"`, (*calls)[0].Query["message"])
	})

	t.Run("existing unmanaged dashboard is refused with the fix", func(t *testing.T) {
		ctx, calls := newGitSyncServer(t, map[string]map[string]interface{}{
			"mine": k8sObject("Dashboard", "mine", unmanaged, map[string]interface{}{"title": "Mine", "panels": []interface{}{}}),
		}, folders)
		_, err := updateDashboardContent(ctx, UpdateDashboardParams{
			Dashboard: map[string]interface{}{"uid": "mine", "title": "Mine", "panels": []interface{}{}},
			FolderUID: "f-synced",
			Overwrite: true,
		})
		require.ErrorContains(t, err, "Delete the existing dashboard first")
		assert.Empty(t, *calls)
	})

	t.Run("unmanaged folder keeps the regular save path", func(t *testing.T) {
		ctx, _ := newGitSyncServer(t, nil, folders)
		res, handled, err := saveFullJSONToRepoIfManaged(ctx, UpdateDashboardParams{
			Dashboard: map[string]interface{}{"uid": "x", "title": "X", "panels": []interface{}{}},
			FolderUID: "f-mine",
		})
		require.NoError(t, err)
		assert.False(t, handled)
		assert.Nil(t, res)
	})
}
