//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
)

const testNotebooksCollectionPath = "/apis/dashboard.grafana.app/v2beta1/namespaces/default/notebooks"

// notebooksTestServer serves /api/frontend/settings (so the namespace resolves)
// and delegates every other request to h.
func notebooksTestServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(withFrontendSettings("default", h))
	t.Cleanup(ts.Close)
	return ts
}

func notebooksTestContext(ts *httptest.Server) context.Context {
	return mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
}

func notebooksTestResource(uid, title string, tags []string, cells int) map[string]any {
	cellItems := make([]any, 0, cells)
	elements := map[string]any{}
	for i := 0; i < cells; i++ {
		name := "markdown"
		if i > 0 {
			name = "markdown-" + string(rune('a'+i))
		}
		elements[name] = map[string]any{
			"kind": "Cell",
			"spec": map[string]any{
				"content": map[string]any{"kind": "Markdown", "spec": map[string]any{"text": "cell"}},
			},
		}
		cellItems = append(cellItems, map[string]any{
			"kind": "NotebookLayoutItem",
			"spec": map[string]any{
				"element": map[string]any{"kind": "ElementReference", "name": name},
				"source":  "user",
			},
		})
	}

	return map[string]any{
		"apiVersion": notebooksAPIVersion,
		"kind":       notebooksKind,
		"metadata": map[string]any{
			"name":              uid,
			"resourceVersion":   "42",
			"creationTimestamp": "2026-01-02T03:04:05Z",
			"labels":            map[string]any{"team": "sre"},
			"annotations":       map[string]any{"grafana.app/createdBy": "user:1"},
		},
		"spec": map[string]any{
			"title": title,
			"tags":  tags,
			"layout": map[string]any{
				"kind": notebookLayoutKind,
				"spec": map[string]any{"cells": cellItems},
			},
			"elements": elements,
		},
	}
}

func TestListNotebooks(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, testNotebooksCollectionPath, r.URL.Path)
		assert.Equal(t, "50", r.URL.Query().Get("limit"))

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"kind": "NotebookList",
			"items": []any{
				notebooksTestResource("nb-1", "CPU saturation", []string{"sre"}, 2),
				notebooksTestResource("nb-2", "Memory saturation", nil, 0),
			},
		}))
	})

	summaries, err := listNotebooks(notebooksTestContext(ts), ListNotebooksParams{Limit: 50})
	require.NoError(t, err)
	require.Len(t, summaries, 2)

	assert.Equal(t, "nb-1", summaries[0].UID)
	assert.Equal(t, "CPU saturation", summaries[0].Title)
	assert.Equal(t, []string{"sre"}, summaries[0].Tags)
	require.NotNil(t, summaries[0].CellCount)
	assert.Equal(t, 2, *summaries[0].CellCount)
	assert.Equal(t, "2026-01-02T03:04:05Z", summaries[0].CreatedAt)
	assert.Equal(t, ts.URL+"/notebooks/nb-1", summaries[0].URL)
	require.NotNil(t, summaries[1].CellCount)
	assert.Equal(t, 0, *summaries[1].CellCount)
}

func TestListNotebooks_Empty(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"kind": "NotebookList", "items": []any{}}))
	})

	summaries, err := listNotebooks(notebooksTestContext(ts), ListNotebooksParams{})
	require.NoError(t, err)
	require.NotNil(t, summaries)
	assert.Empty(t, summaries)
}

// The notebooks API is namespaced, and the namespace comes from the target
// Grafana rather than an argument, so a Cloud stack routes to its own namespace.
func TestListNotebooks_NamespaceFromSettings(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(withFrontendSettings("stacks-12345", func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"items": []any{}}))
	}))
	defer ts.Close()

	_, err := listNotebooks(notebooksTestContext(ts), ListNotebooksParams{})
	require.NoError(t, err)
	assert.Equal(t, "/apis/dashboard.grafana.app/v2beta1/namespaces/stacks-12345/notebooks", capturedPath)
}

// TestNotebookLinksUsePublicURL covers the host half of the link problem: an
// MCP server usually reaches Grafana on an internal address (an in-cluster
// Service, a private DNS name), but the URLs these tools return are meant to be
// clicked by a person. Grafana publishes the browser-facing address as appUrl in
// /api/frontend/settings, and every other tool that emits user-facing links
// prefers it over the configured URL - notebooks did not, so every link pointed
// somewhere a browser cannot resolve.
func TestNotebookLinksUsePublicURL(t *testing.T) {
	const publicURL = "https://grafana.example.com/grafana"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/frontend/settings":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"namespace": "default", "appUrl": publicURL})
		case strings.HasSuffix(r.URL.Path, "/search"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"resource": map[string]any{"name": "nb-1"},
				"fields":   map[string]any{"title": "CPU saturation", "tags": []string{"sre"}},
			}}})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{notebooksTestResource("nb-1", "CPU saturation", []string{"sre"}, 1)}})
		}
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL})
	ctx = mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.NewGrafanaClient(ctx, ts.URL, "grafana-token", nil))

	listed, err := listNotebooks(ctx, ListNotebooksParams{})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, publicURL+"/notebooks/nb-1", listed[0].URL,
		"list links must use appUrl, not the internal URL the MCP server talks to")

	found, err := searchNotebooks(ctx, SearchNotebooksParams{Query: "saturation"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, publicURL+"/notebooks/nb-1", found[0].URL)
}

func TestGetNotebookByUID(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, testNotebooksCollectionPath+"/nb-1", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-1", "CPU saturation", nil, 1)))
	})

	body, err := getNotebookByUID(notebooksTestContext(ts), GetNotebookByUIDParams{UID: "nb-1"})
	require.NoError(t, err)

	var nb map[string]any
	require.NoError(t, json.Unmarshal(body, &nb))
	assert.Equal(t, "Notebook", nb["kind"])
	assert.Equal(t, "nb-1", nb["metadata"].(map[string]any)["name"])
}

func TestGetNotebookByUID_RequiresUID(t *testing.T) {
	_, err := getNotebookByUID(context.Background(), GetNotebookByUIDParams{})
	require.ErrorContains(t, err, "uid is required")
}

func TestSearchNotebooks(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, testNotebooksCollectionPath+"/search", r.URL.Path)

		var query map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&query))
		assert.Equal(t, "search.grafana.app/v0alpha1", query["apiVersion"])
		assert.Equal(t, "SearchQuery", query["kind"])
		assert.Equal(t, float64(5), query["limit"])
		where := query["where"].(map[string]any)
		assert.Equal(t, "saturation", where["text"].(map[string]any)["value"])
		// Grafana's search returns only a default projection (title, folder)
		// unless the extra fields are requested, so a search that reads tags
		// without naming them here silently returns none. Verified live: the same
		// query with and without this key returns the notebook with and without
		// its tags.
		assert.Equal(t, []any{"title", "tags"}, query["fields"],
			"tags must be requested explicitly or every match reports none")

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"kind": "SearchResults",
			"items": []any{
				map[string]any{
					"resource": map[string]any{
						"group":    "dashboard.grafana.app",
						"resource": "notebooks",
						"kind":     "Notebook",
						"name":     "nb-1",
					},
					"fields": map[string]any{"title": "CPU saturation", "tags": []string{"sre"}},
				},
			},
		}))
	})

	summaries, err := searchNotebooks(notebooksTestContext(ts), SearchNotebooksParams{Query: "saturation", Limit: 5})
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, "nb-1", summaries[0].UID)
	assert.Equal(t, "CPU saturation", summaries[0].Title)
	assert.Equal(t, []string{"sre"}, summaries[0].Tags)
	assert.Equal(t, ts.URL+"/notebooks/nb-1", summaries[0].URL)
	assert.Nil(t, summaries[0].CellCount, "search results do not carry the cell count")
}

func TestSearchNotebooks_RequiresQuery(t *testing.T) {
	_, err := searchNotebooks(context.Background(), SearchNotebooksParams{})
	require.ErrorContains(t, err, "query is required")
}

func TestCreateNotebook(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, testNotebooksCollectionPath, r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, notebooksAPIVersion, body["apiVersion"])
		assert.Equal(t, notebooksKind, body["kind"])
		assert.Equal(t, "notebook-", body["metadata"].(map[string]any)["generateName"])

		spec := body["spec"].(map[string]any)
		assert.Equal(t, "CPU saturation", spec["title"])
		assert.Equal(t, []any{"sre"}, spec["tags"])
		assert.Equal(t, "now-6h", spec["timeSettings"].(map[string]any)["from"])

		layout := spec["layout"].(map[string]any)
		assert.Equal(t, notebookLayoutKind, layout["kind"])
		cells := layout["spec"].(map[string]any)["cells"].([]any)
		require.Len(t, cells, 2)

		elements := spec["elements"].(map[string]any)
		require.NotContains(t, elements, "markdown")
		require.Contains(t, elements, "markdown-1")
		require.Contains(t, elements, "code-1")

		markdown := elements["markdown-1"].(map[string]any)["spec"].(map[string]any)["content"].(map[string]any)
		assert.Equal(t, "Markdown", markdown["kind"])
		assert.Equal(t, "# Findings", markdown["spec"].(map[string]any)["text"])

		code := elements["code-1"].(map[string]any)["spec"].(map[string]any)["content"].(map[string]any)
		assert.Equal(t, "Code", code["kind"])
		assert.Equal(t, "rate(errors_total[5m])", code["spec"].(map[string]any)["code"])
		assert.Equal(t, "promql", code["spec"].(map[string]any)["language"])

		// Cells keep their document order and are authored by the caller.
		for i, name := range []string{"markdown-1", "code-1"} {
			item := cells[i].(map[string]any)["spec"].(map[string]any)
			assert.Equal(t, "NotebookLayoutItem", cells[i].(map[string]any)["kind"])
			assert.Equal(t, "user", item["source"])
			assert.Equal(t, map[string]any{"kind": "ElementReference", "name": name}, item["element"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-new", "CPU saturation", []string{"sre"}, 1)))
	})

	body, err := createNotebook(notebooksTestContext(ts), CreateNotebookParams{
		Title: "CPU saturation",
		Tags:  []string{"sre"},
		Cells: []notebookCell{
			{Kind: "markdown", Text: "# Findings"},
			{Kind: "code", Code: "rate(errors_total[5m])"},
		},
	})
	require.NoError(t, err)

	var nb map[string]any
	require.NoError(t, json.Unmarshal(body, &nb))
	assert.Equal(t, "nb-new", nb["metadata"].(map[string]any)["name"])
}

func TestCreateNotebook_WithoutCells(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		spec := body["spec"].(map[string]any)
		assert.Empty(t, spec["elements"].(map[string]any))
		assert.Empty(t, spec["layout"].(map[string]any)["spec"].(map[string]any)["cells"].([]any))

		w.WriteHeader(http.StatusCreated)
		require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-new", "Empty", nil, 0)))
	})

	_, err := createNotebook(notebooksTestContext(ts), CreateNotebookParams{Title: "Empty"})
	require.NoError(t, err)
}

// Cell kinds are validated before the request is made, so a bad cell fails with
// a message naming the cell instead of an API error.
func TestCreateNotebook_RejectsInvalidCells(t *testing.T) {
	tests := []struct {
		name string
		cell notebookCell
		want string
	}{
		{name: "unknown kind", cell: notebookCell{Kind: "panel"}, want: "unsupported cell kind"},
		{name: "markdown without text", cell: notebookCell{Kind: "markdown"}, want: "has no text"},
		{name: "code without code", cell: notebookCell{Kind: "code"}, want: "has no code"},
		{name: "missing kind", cell: notebookCell{Text: "hi"}, want: "unsupported cell kind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := createNotebook(context.Background(), CreateNotebookParams{Title: "x", Cells: []notebookCell{tt.cell}})
			require.ErrorContains(t, err, tt.want)
			require.ErrorContains(t, err, "cell 1")
		})
	}
}

// A code cell defaults to promql, which is what the UI gives a new code cell.
func TestCreateNotebook_CodeCellDefaultsLanguage(t *testing.T) {
	var spec map[string]any
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		spec = body["spec"].(map[string]any)

		w.WriteHeader(http.StatusCreated)
		require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-new", "Code", nil, 1)))
	})

	_, err := createNotebook(notebooksTestContext(ts), CreateNotebookParams{
		Title: "Code",
		Cells: []notebookCell{{Kind: "code", Code: "1+1"}},
	})
	require.NoError(t, err)

	code := spec["elements"].(map[string]any)["code-1"].(map[string]any)["spec"].(map[string]any)["content"].(map[string]any)
	assert.Equal(t, "promql", code["spec"].(map[string]any)["language"])
}

func TestCreateNotebook_RequiresTitle(t *testing.T) {
	_, err := createNotebook(context.Background(), CreateNotebookParams{})
	require.ErrorContains(t, err, "title is required")
}

// The notebooks API replaces the whole resource, so an update must carry the
// existing metadata back: its labels and annotations are not part of the spec
// the caller edits, and dropping them would strip a notebook's creator record.
func TestUpdateNotebook_PreservesMetadata(t *testing.T) {
	var requests int
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		assert.Equal(t, testNotebooksCollectionPath+"/nb-1", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			assert.Equal(t, http.MethodGet, r.Method)
			require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-1", "Old title", nil, 1)))
		case 2:
			assert.Equal(t, http.MethodPut, r.Method)

			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, notebooksAPIVersion, body["apiVersion"])
			assert.Equal(t, notebooksKind, body["kind"])

			metadata := body["metadata"].(map[string]any)
			assert.Equal(t, "nb-1", metadata["name"])
			assert.Equal(t, "42", metadata["resourceVersion"])
			assert.Equal(t, map[string]any{"team": "sre"}, metadata["labels"])
			assert.Equal(t, map[string]any{"grafana.app/createdBy": "user:1"}, metadata["annotations"])

			assert.Equal(t, "New title", body["spec"].(map[string]any)["title"])

			require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-1", "New title", nil, 1)))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	})

	body, err := updateNotebook(notebooksTestContext(ts), UpdateNotebookParams{
		UID:  "nb-1",
		Spec: map[string]any{"title": "New title"},
	})
	require.NoError(t, err)

	var nb map[string]any
	require.NoError(t, json.Unmarshal(body, &nb))
	assert.Equal(t, "New title", nb["spec"].(map[string]any)["title"])
	assert.Equal(t, 2, requests)
}

// A caller that read the notebook at one resourceVersion must not silently
// overwrite a newer one; the update fails before writing anything.
func TestUpdateNotebook_RejectsStaleResourceVersion(t *testing.T) {
	var wrote bool
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			wrote = true
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-1", "Current title", nil, 1)))
	})

	_, err := updateNotebook(notebooksTestContext(ts), UpdateNotebookParams{
		UID:                     "nb-1",
		Spec:                    map[string]any{"title": "New title"},
		ExpectedResourceVersion: "7",
	})
	require.ErrorContains(t, err, "changed since it was read")
	require.ErrorContains(t, err, "at resourceVersion 42, expected 7")
	assert.False(t, wrote, "a stale update must not reach the API")
}

func TestUpdateNotebook_AcceptsMatchingResourceVersion(t *testing.T) {
	var requests int
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-1", body["spec"].(map[string]any)["title"].(string), nil, 1)))
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-1", "Current title", nil, 1)))
	})

	body, err := updateNotebook(notebooksTestContext(ts), UpdateNotebookParams{
		UID:                     "nb-1",
		Spec:                    map[string]any{"title": "New title"},
		ExpectedResourceVersion: "42",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, requests)

	var nb map[string]any
	require.NoError(t, json.Unmarshal(body, &nb))
	assert.Equal(t, "New title", nb["spec"].(map[string]any)["title"])
}

func TestUpdateNotebook_RequiresSpec(t *testing.T) {
	_, err := updateNotebook(context.Background(), UpdateNotebookParams{UID: "nb-1"})
	require.ErrorContains(t, err, "spec is required")
}

// update_notebook reads the notebook before writing it, so a token holding only
// the write actions gets a 403 on the GET and the update never happens.
func TestUpdateNotebook_DeclaresReadPermissions(t *testing.T) {
	perms, ok := mcpgrafana.ToolRequiredPermissions(UpdateNotebook.Tool)
	require.True(t, ok, "update_notebook declares no RBAC permissions")
	assert.Subset(t, perms, []string{"notebooks:read", "dashboard.grafana.app/notebooks:get"})
}

func TestDeleteNotebook(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, testNotebooksCollectionPath+"/nb-1", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"kind":"Status","status":"Success"}`))
		require.NoError(t, err)
	})

	msg, err := deleteNotebook(notebooksTestContext(ts), DeleteNotebookParams{UID: "nb-1"})
	require.NoError(t, err)
	assert.Equal(t, "Notebook nb-1 deleted successfully", msg)
}

// A Grafana that has not enabled notebooks denies the call, so the error has to
// name the toggle rather than look like a permissions problem.
func TestNotebooks_FeatureDisabled(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, err := w.Write([]byte(`{"kind":"Status","message":"notebooks feature is not enabled"}`))
		require.NoError(t, err)
	})

	_, err := listNotebooks(notebooksTestContext(ts), ListNotebooksParams{})
	require.ErrorContains(t, err, "notebooks feature is not enabled")
	require.ErrorContains(t, err, "dashboard.notebooks feature toggle")
}

func TestNotebooks_NotFound(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, err := w.Write([]byte("not found"))
		require.NoError(t, err)
	})

	_, err := getNotebookByUID(notebooksTestContext(ts), GetNotebookByUIDParams{UID: "missing"})
	require.ErrorContains(t, err, "HTTP 404")
}

// A stalled notebooks call must give up on the configured timeout rather than
// hang the tool.
func TestNotebooks_HonoursConfiguredTimeout(t *testing.T) {
	ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
		}
	})

	cfg := mcpgrafana.GrafanaConfig{URL: ts.URL, Timeout: 50 * time.Millisecond}
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)

	start := time.Now()
	_, err := listNotebooks(ctx, ListNotebooksParams{})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 500*time.Millisecond, "the request should not wait for the server")
}

func TestNotebooks_NoURLConfigured(t *testing.T) {
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{})
	_, err := listNotebooks(ctx, ListNotebooksParams{})
	require.ErrorContains(t, err, "grafana URL is not configured")
}

// Tools that call /apis must send the credentials from the context, in every
// auth mode the server supports.
func TestNotebooks_Auth(t *testing.T) {
	tests := []struct {
		name  string
		cfg   mcpgrafana.GrafanaConfig
		check func(t *testing.T, r *http.Request)
	}{
		{
			name: "service account token",
			cfg:  mcpgrafana.GrafanaConfig{APIKey: "test-api-key"},
			check: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
			},
		},
		{
			name: "basic auth",
			cfg:  mcpgrafana.GrafanaConfig{BasicAuth: url.UserPassword("admin", "admin")},
			check: func(t *testing.T, r *http.Request) {
				username, password, ok := r.BasicAuth()
				require.True(t, ok)
				assert.Equal(t, "admin", username)
				assert.Equal(t, "admin", password)
			},
		},
		{
			name: "on-behalf-of",
			cfg:  mcpgrafana.GrafanaConfig{AccessToken: "access-token", IDToken: "id-token"},
			check: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "access-token", r.Header.Get("X-Access-Token"))
				assert.Equal(t, "id-token", r.Header.Get("X-Grafana-Id"))
			},
		},
		{
			name: "org id",
			cfg:  mcpgrafana.GrafanaConfig{APIKey: "test-api-key", OrgID: 7},
			check: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "7", r.Header.Get("X-Grafana-Org-Id"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := notebooksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				tt.check(t, r)
				w.Header().Set("Content-Type", "application/json")
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"items": []any{}}))
			})

			cfg := tt.cfg
			cfg.URL = ts.URL
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), cfg)

			_, err := listNotebooks(ctx, ListNotebooksParams{})
			require.NoError(t, err)
		})
	}
}

// A base path in GRAFANA_URL (a Grafana served under a subpath) has to be kept.
func TestNotebooks_BasePath(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"items": []any{}}))
	}))
	defer ts.Close()

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL + "/grafana"})
	_, err := listNotebooks(ctx, ListNotebooksParams{})
	require.NoError(t, err)
	assert.Equal(t, "/grafana/apis/dashboard.grafana.app/v2beta1/namespaces/default/notebooks", capturedPath)
}

// renderNotebookTestServer serves the notebooks API for nb-1 and hands the
// /render/notebooks/<uid>/render export to render, which is where the image
// renderer would answer.
func renderNotebookTestServer(t *testing.T, render http.HandlerFunc) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasPrefix(r.URL.Path, "/render/") {
			render(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(notebooksTestResource("nb-1", "Dex build info", []string{"sre"}, 2)))
	}))
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestRenderNotebookPDF(t *testing.T) {
	pdf := []byte("%PDF-1.4 fake notebook pdf")
	ts, paths := renderNotebookTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/render/notebooks/nb-1/render", r.URL.Path)

		query := r.URL.Query()
		assert.Equal(t, "pdf", query.Get("encoding"))
		assert.Equal(t, "now-6h", query.Get("from"))
		assert.Equal(t, "now", query.Get("to"))
		assert.Equal(t, "Europe/Amsterdam", query.Get("timezone"))
		assert.Equal(t, "Europe/Amsterdam", query.Get("tz"))
		assert.Equal(t, "light", query.Get("theme"))
		// No per-call org on this config, so nothing is scoped: the renderer must
		// not be told an org it was never given. orgId is never the answer either -
		// it makes Grafana persist an org switch via /api/user/using (see
		// TestRenderNotebookPDF_TargetOrgID).
		assert.Empty(t, query.Get("targetOrgId"))
		assert.Empty(t, query.Get("orgId"), "orgId switches the caller's org; the render backend reads targetOrgId")

		w.Header().Set("Content-Type", "application/pdf")
		_, err := w.Write(pdf)
		require.NoError(t, err)
	})

	result, err := renderNotebookPDF(notebooksTestContext(ts), RenderNotebookPDFParams{
		UID:      "nb-1",
		From:     "now-6h",
		To:       "now",
		TimeZone: "Europe/Amsterdam",
		Theme:    "light",
	})
	require.NoError(t, err)

	// The notebook is resolved through the API before the page is exported, so a
	// bad UID cannot come back as a PDF of Grafana's error page.
	notebookAt := slices.Index(*paths, testNotebooksCollectionPath+"/nb-1")
	require.NotEqual(t, -1, notebookAt, "the notebook should be fetched: %v", *paths)
	assert.Greater(t, slices.Index(*paths, "/render/notebooks/nb-1/render"), notebookAt)

	require.Len(t, result.Content, 2)
	summary, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "the summary comes first so clients that drop resource blobs still say what happened")
	assert.Contains(t, summary.Text, `"Dex build info"`)
	assert.Contains(t, summary.Text, "nb-1")
	assert.Contains(t, summary.Text, "Europe/Amsterdam")
	assert.Contains(t, summary.Text, ts.URL+"/notebooks/nb-1")

	resource, ok := result.Content[1].(*mcp.EmbeddedResource)
	require.True(t, ok)
	require.NotNil(t, resource.Resource)
	assert.Equal(t, "grafana://notebooks/nb-1.pdf", resource.Resource.URI)
	assert.Equal(t, "application/pdf", resource.Resource.MIMEType)
	assert.Equal(t, pdf, resource.Resource.Blob)
}

func TestRenderNotebookPDF_RequiresUID(t *testing.T) {
	_, err := renderNotebookPDF(context.Background(), RenderNotebookPDFParams{})
	require.ErrorContains(t, err, "uid is required")
}

func TestRenderNotebookPDF_RejectsUnknownTheme(t *testing.T) {
	_, err := renderNotebookPDF(context.Background(), RenderNotebookPDFParams{UID: "nb-1", Theme: "solarized"})
	require.ErrorContains(t, err, "theme must be light or dark")
}

func TestNotebookRenderURL(t *testing.T) {
	url := notebookRenderURL(mcpgrafana.GrafanaConfig{URL: "https://gf.example.com/"}, RenderNotebookPDFParams{UID: "nb-1"})
	assert.Equal(t, "https://gf.example.com/render/notebooks/nb-1/render?encoding=pdf", url,
		"from/to/tz/theme stay unset when the caller does not pass them, so Grafana's defaults apply")
}

// TestRenderNotebookPDF_TargetOrgID pins the org routing end to end, because the
// two halves of it look interchangeable and are not. The notebook itself is
// fetched with X-Grafana-Org-Id (that is how the transport scopes every API
// call), but the /render export is driven by the image renderer, which ignores
// that header and reads the org from targetOrgId on the query string. Scoping
// only the header therefore lets this tool GET the right notebook and still
// return a 200 PDF of a different org's page (or of Grafana's not-found view),
// since UIDs are unique only within an org. get_panel_image sets targetOrgId for
// the same reason, and deliberately not orgId - the frontend persists orgId as
// an org switch via /api/user/using.
func TestRenderNotebookPDF_TargetOrgID(t *testing.T) {
	pdf := []byte("%PDF-1.4 fake notebook pdf")
	ts, _ := renderNotebookTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "2", r.URL.Query().Get("targetOrgId"),
			"the renderer reads the org from targetOrgId; the header the API call uses is ignored on this path")
		assert.Empty(t, r.URL.Query().Get("orgId"),
			"orgId would persist an org switch on the caller's identity")
		w.Header().Set("Content-Type", "application/pdf")
		_, err := w.Write(pdf)
		require.NoError(t, err)
	})

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: ts.URL, OrgID: 2})
	result, err := renderNotebookPDF(ctx, RenderNotebookPDFParams{UID: "nb-1"})
	require.NoError(t, err)
	require.Len(t, result.Content, 2)
	resource, ok := result.Content[1].(*mcp.EmbeddedResource)
	require.True(t, ok)
	require.NotNil(t, resource.Resource)
	assert.Equal(t, pdf, resource.Resource.Blob)
}

// The export renders the whole page, including whatever Grafana put in it, so a
// missing notebook has to be caught before rendering: the route answers 200 with
// a PDF of the error page.
func TestRenderNotebookPDF_NotFoundBeforeRendering(t *testing.T) {
	var rendered bool
	ts := httptest.NewServer(withFrontendSettings("default", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/render/") {
			rendered = true
			w.Header().Set("Content-Type", "application/pdf")
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, err := w.Write([]byte(`{"kind":"Status","reason":"NotFound"}`))
		require.NoError(t, err)
	}))
	defer ts.Close()

	_, err := renderNotebookPDF(notebooksTestContext(ts), RenderNotebookPDFParams{UID: "missing"})
	require.ErrorContains(t, err, "HTTP 404")
	assert.False(t, rendered, "nothing should be rendered when the notebook does not exist")
}

// Grafana answers an unauthorized export by redirecting to /login, so the body
// is an HTML page with a 200 status by the time the client sees it.
func TestRenderNotebookPDF_RejectsNonPDF(t *testing.T) {
	ts, _ := renderNotebookTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, err := w.Write([]byte("<html>Login</html>"))
		require.NoError(t, err)
	})

	_, err := renderNotebookPDF(notebooksTestContext(ts), RenderNotebookPDFParams{UID: "nb-1"})
	require.ErrorContains(t, err, "expected a PDF")
	require.ErrorContains(t, err, "text/html")
}

func TestRenderNotebookPDF_RendererMissing(t *testing.T) {
	ts, _ := renderNotebookTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, err := w.Write([]byte(`{"message":"Render request failed"}`))
		require.NoError(t, err)
	})

	_, err := renderNotebookPDF(notebooksTestContext(ts), RenderNotebookPDFParams{UID: "nb-1"})
	require.ErrorContains(t, err, "image renderer not available")
}

// The export runs in a headless browser, so it needs its own budget instead of
// the Grafana client timeout used by the API calls.
func TestRenderNotebookPDF_HonoursTimeout(t *testing.T) {
	ts, _ := renderNotebookTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	})

	timeout := 1
	start := time.Now()
	_, err := renderNotebookPDF(notebooksTestContext(ts), RenderNotebookPDFParams{UID: "nb-1", Timeout: &timeout})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "the render should give up on the requested timeout")
}
