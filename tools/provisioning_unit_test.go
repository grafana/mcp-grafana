//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// repositoryListFixture mirrors a trimmed real response from
// /apis/provisioning.grafana.app/v0alpha1/namespaces/<ns>/repositories.
func repositoryListFixture() map[string]any {
	return map[string]any{
		"apiVersion": "provisioning.grafana.app/v0alpha1",
		"kind":       "RepositoryList",
		"items": []any{
			map[string]any{
				"metadata": map[string]any{"name": "git-global"},
				"spec": map[string]any{
					"title": "GitSync - Global",
					"type":  "github",
					"github": map[string]any{
						"url":    "https://github.com/example/dashboards",
						"branch": "main",
						"path":   "dashboards/global",
					},
					"sync":      map[string]any{"enabled": true},
					"workflows": []string{"branch"},
				},
				"status": map[string]any{
					"health": map[string]any{"healthy": true},
					"sync":   map[string]any{"state": "success"},
				},
			},
			map[string]any{
				"metadata": map[string]any{"name": "local-staging"},
				"spec": map[string]any{
					"title": "Staging local",
					"type":  "local",
					"local": map[string]any{"path": "/etc/dashboards"},
					"sync":  map[string]any{"enabled": false},
				},
				"status": map[string]any{
					"health": map[string]any{"healthy": false},
					"sync":   map[string]any{"state": "pending"},
				},
			},
		},
	}
}

func TestListProvisioningRepositories_DefaultNamespace(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(repositoryListFixture())
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	out, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{})

	require.NoError(t, err)
	assert.Equal(t, "/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories", capturedPath)
	require.Len(t, out, 2)

	gh := out[0]
	assert.Equal(t, "git-global", gh.Name)
	assert.Equal(t, "GitSync - Global", gh.Title)
	assert.Equal(t, "github", gh.Type)
	assert.Equal(t, "https://github.com/example/dashboards", gh.URL)
	assert.Equal(t, "main", gh.Branch)
	assert.Equal(t, "dashboards/global", gh.Path)
	assert.True(t, gh.SyncEnabled)
	assert.Equal(t, []string{"branch"}, gh.Workflows)
	assert.True(t, gh.Healthy)
	assert.Equal(t, "success", gh.SyncState)

	local := out[1]
	assert.Equal(t, "local-staging", local.Name)
	assert.Equal(t, "local", local.Type)
	assert.Equal(t, "/etc/dashboards", local.Path)
	assert.Empty(t, local.URL)
	assert.Empty(t, local.Branch)
	assert.False(t, local.SyncEnabled)
	assert.False(t, local.Healthy)
	assert.Equal(t, "pending", local.SyncState)
}

func TestListProvisioningRepositories_CustomNamespace(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	out, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{Namespace: "stacks-123"})

	require.NoError(t, err)
	assert.Equal(t, "/apis/provisioning.grafana.app/v0alpha1/namespaces/stacks-123/repositories", capturedPath)
	assert.Empty(t, out)
}

func TestListProvisioningRepositories_NoURLConfigured(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
	_, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "grafana URL is not configured")
}

func TestListProvisioningRepositories_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"forbidden"}`))
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	_, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}
