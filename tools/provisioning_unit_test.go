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
			map[string]any{
				"metadata": map[string]any{"name": "git-plain"},
				"spec": map[string]any{
					"title": "Plain Git",
					"type":  "git",
					"git": map[string]any{
						"url":    "https://git.example.com/dashboards.git",
						"branch": "develop",
						"path":   "grafana",
					},
					"sync": map[string]any{"enabled": true},
				},
				"status": map[string]any{
					"health": map[string]any{"healthy": true},
					"sync":   map[string]any{"state": "success"},
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
	require.Len(t, out, 3)

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

	git := out[2]
	assert.Equal(t, "git-plain", git.Name)
	assert.Equal(t, "git", git.Type)
	assert.Equal(t, "https://git.example.com/dashboards.git", git.URL)
	assert.Equal(t, "develop", git.Branch)
	assert.Equal(t, "grafana", git.Path)
	assert.True(t, git.SyncEnabled)
	assert.True(t, git.Healthy)
	assert.Equal(t, "success", git.SyncState)
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

func TestListProvisioningRepositories_RejectsTraversalNamespace(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.invalid"})
	_, err := listProvisioningRepositories(ctx, ListProvisioningRepositoriesParams{Namespace: ".."})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace must not be a relative-directory reference")
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

// -----------------------------------------------------------------------------
// validate_provisioning_file
// -----------------------------------------------------------------------------

func TestValidateProvisioningFile_Valid(t *testing.T) {
	var capturedURI string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"path": "folder/dash.json",
			"resource": map[string]any{
				"action": "create",
				"type": map[string]any{
					"group":    "dashboard.grafana.app",
					"kind":     "Dashboard",
					"resource": "dashboards",
					"version":  "v2",
				},
			},
		})
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	out, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{
		Repo: "git-global",
		Path: "folder/dash.json",
		Ref:  "feature/branch",
	})

	require.NoError(t, err)
	assert.Equal(t, "/apis/provisioning.grafana.app/v0alpha1/namespaces/default/repositories/git-global/files/folder/dash.json?ref=feature%2Fbranch", capturedURI)
	assert.True(t, out.Valid)
	assert.Equal(t, "create", out.Action)
	require.NotNil(t, out.Type)
	assert.Equal(t, "Dashboard", out.Type.Kind)
	assert.Empty(t, out.Errors)
}

func TestValidateProvisioningFile_AdmissionInvalid(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":       "Status",
			"apiVersion": "v1",
			"status":     "Failure",
			"message":    `Dashboard.dashboard.grafana.app "x" is invalid: ...`,
			"reason":     "Invalid",
			"code":       422,
			"details": map[string]any{
				"group": "dashboard.grafana.app",
				"kind":  "Dashboard",
				"name":  "x",
				"causes": []any{
					map[string]any{
						"reason":  "FieldValueInvalid",
						"message": "incompatible list lengths",
						"field":   "DashboardSpec.variables",
					},
					map[string]any{
						"reason":  "FieldValueInvalid",
						"message": `conflicting values "AdhocVariable" and "QueryVariable"`,
						"field":   "DashboardSpec.variables.0.kind",
					},
				},
			},
		})
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	out, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{
		Repo: "git-global",
		Path: "broken.json",
	})

	require.NoError(t, err)
	assert.False(t, out.Valid)
	require.Len(t, out.Errors, 2)
	assert.Equal(t, "DashboardSpec.variables", out.Errors[0].Field)
	assert.Equal(t, "FieldValueInvalid", out.Errors[0].Reason)
	assert.Contains(t, out.Errors[0].Message, "incompatible list lengths")
	assert.Equal(t, "DashboardSpec.variables.0.kind", out.Errors[1].Field)
}

func TestValidateProvisioningFile_StatusWithoutCauses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":    "Status",
			"status":  "Failure",
			"message": "could not parse: unexpected end of JSON input",
			"reason":  "BadRequest",
		})
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	out, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{
		Repo: "git-global",
		Path: "bad.json",
	})

	require.NoError(t, err)
	assert.False(t, out.Valid)
	require.Len(t, out.Errors, 1)
	assert.Equal(t, "could not parse: unexpected end of JSON input", out.Errors[0].Message)
	assert.Equal(t, "BadRequest", out.Errors[0].Reason)
}

func TestValidateProvisioningFile_RejectsTraversal(t *testing.T) {
	cases := []struct {
		name string
		args ValidateProvisioningFileParams
		want string
	}{
		{"missing repo", ValidateProvisioningFileParams{Path: "dash.json"}, "repo is required"},
		{"missing path", ValidateProvisioningFileParams{Repo: "ok"}, "path is required"},
		{"repo with slash", ValidateProvisioningFileParams{Repo: "a/b", Path: "x.json"}, "must not contain path separators"},
		{"repo is ..", ValidateProvisioningFileParams{Repo: "..", Path: "x.json"}, "must not be a relative-directory reference"},
		{"repo is .", ValidateProvisioningFileParams{Repo: ".", Path: "x.json"}, "must not be a relative-directory reference"},
		{"path with ..", ValidateProvisioningFileParams{Repo: "ok", Path: "a/../b.json"}, "must not contain relative-directory segments"},
		{"path with .", ValidateProvisioningFileParams{Repo: "ok", Path: "a/./b.json"}, "must not contain relative-directory segments"},
		{"path with backslash ..", ValidateProvisioningFileParams{Repo: "ok", Path: `a\..\b.json`}, "must not contain relative-directory segments"},
		{"path is a single slash", ValidateProvisioningFileParams{Repo: "ok", Path: "/"}, "must reference a file"},
		{"path is only slashes", ValidateProvisioningFileParams{Repo: "ok", Path: "///"}, "must reference a file"},
		{"namespace is ..", ValidateProvisioningFileParams{Namespace: "..", Repo: "ok", Path: "x.json"}, "namespace must not be a relative-directory reference"},
		{"namespace with slash", ValidateProvisioningFileParams{Namespace: "a/b", Repo: "ok", Path: "x.json"}, "namespace must not contain path separators"},
	}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: "http://example.invalid"})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateProvisioningFile(ctx, tc.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestValidateProvisioningFile_NonStatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<html>oops</html>`))
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	_, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{Repo: "ok", Path: "x.json"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

// A 5xx that carries a k8s Status body is a transient server failure, not a
// verdict on the file: it must surface as a tool error, not valid:false.
func TestValidateProvisioningFile_ServerErrorStatusIsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":    "Status",
			"status":  "Failure",
			"message": "internal server error",
			"reason":  "InternalError",
			"code":    500,
		})
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	_, err := validateProvisioningFile(ctx, ValidateProvisioningFileParams{Repo: "ok", Path: "x.json"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}
