// Requires a Grafana instance that serves the notebooks API (Grafana 13.3 or
// later with the dashboard.notebooks feature toggle enabled). The docker-compose
// images do not, so these tests skip there. Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireNotebooksAPI skips the test when the Grafana under test does not serve
// the notebooks API, which is still behind a feature toggle.
func requireNotebooksAPI(t *testing.T, ctx context.Context) {
	t.Helper()
	_, err := listNotebooks(ctx, ListNotebooksParams{})
	if err != nil && (strings.Contains(err.Error(), "notebooks feature is not enabled") ||
		strings.Contains(err.Error(), "HTTP 404")) {
		t.Skipf("notebooks API is not available on this Grafana instance: %v", err)
	}
	require.NoError(t, err)
}

func TestNotebookTools(t *testing.T) {
	ctx := newTestContext()
	requireNotebooksAPI(t, ctx)

	title := fmt.Sprintf("Integration Test Notebook %d", time.Now().UnixNano())

	created, err := createNotebook(ctx, CreateNotebookParams{
		Title: title,
		Tags:  []string{"mcp-integration-test"},
		Cells: []notebookCell{
			{Kind: "markdown", Text: "Created by the notebooks integration test."},
			{Kind: "code", Code: "1+1"},
		},
	})
	require.NoError(t, err)

	var createdResource notebookResource
	require.NoError(t, json.Unmarshal(created, &createdResource))
	uid := createdResource.Metadata.Name
	require.NotEmpty(t, uid)

	t.Cleanup(func() {
		if uid == "" {
			return
		}
		_, err := deleteNotebook(ctx, DeleteNotebookParams{UID: uid})
		assert.NoError(t, err, "clean up notebook %s", uid)
	})

	t.Run("create returns the notebook spec", func(t *testing.T) {
		var spec notebookSpecFields
		require.NoError(t, json.Unmarshal(createdResource.Spec, &spec))
		assert.Equal(t, title, spec.Title)
		assert.Equal(t, []string{"mcp-integration-test"}, spec.Tags)
		assert.Len(t, spec.Layout.Spec.Cells, 2)
	})

	t.Run("get returns the notebook", func(t *testing.T) {
		body, err := getNotebookByUID(ctx, GetNotebookByUIDParams{UID: uid})
		require.NoError(t, err)

		var nb notebookResource
		require.NoError(t, json.Unmarshal(body, &nb))
		assert.Equal(t, uid, nb.Metadata.Name)
	})

	t.Run("list includes the notebook", func(t *testing.T) {
		summaries, err := listNotebooks(ctx, ListNotebooksParams{})
		require.NoError(t, err)

		found := false
		for _, summary := range summaries {
			if summary.UID == uid {
				found = true
				assert.Equal(t, title, summary.Title)
				require.NotNil(t, summary.CellCount)
				assert.Equal(t, 2, *summary.CellCount)
			}
		}
		assert.True(t, found, "expected notebook %s in the list", uid)
	})

	t.Run("search finds the notebook", func(t *testing.T) {
		summaries, err := searchNotebooks(ctx, SearchNotebooksParams{Query: title})
		require.NoError(t, err)

		found := false
		for _, summary := range summaries {
			if summary.UID == uid {
				found = true
			}
		}
		assert.True(t, found, "expected notebook %s in the search results", uid)
	})

	t.Run("update replaces the spec and keeps metadata", func(t *testing.T) {
		body, err := getNotebookByUID(ctx, GetNotebookByUIDParams{UID: uid})
		require.NoError(t, err)

		var nb notebookResource
		require.NoError(t, json.Unmarshal(body, &nb))

		var spec map[string]any
		require.NoError(t, json.Unmarshal(nb.Spec, &spec))
		updatedTitle := title + " (updated)"
		spec["title"] = updatedTitle

		updated, err := updateNotebook(ctx, UpdateNotebookParams{
			UID:                     uid,
			Spec:                    spec,
			ExpectedResourceVersion: nb.Metadata.ResourceVersion,
		})
		require.NoError(t, err)

		var updatedResource notebookResource
		require.NoError(t, json.Unmarshal(updated, &updatedResource))

		var updatedSpec notebookSpecFields
		require.NoError(t, json.Unmarshal(updatedResource.Spec, &updatedSpec))
		assert.Equal(t, updatedTitle, updatedSpec.Title)

		assert.Equal(t, nb.Metadata.Labels, updatedResource.Metadata.Labels)
		assert.Equal(t, nb.Metadata.Annotations, updatedResource.Metadata.Annotations)
	})

	t.Run("delete removes the notebook", func(t *testing.T) {
		msg, err := deleteNotebook(ctx, DeleteNotebookParams{UID: uid})
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("Notebook %s deleted successfully", uid), msg)

		_, err = getNotebookByUID(ctx, GetNotebookByUIDParams{UID: uid})
		require.Error(t, err)
		require.ErrorContains(t, err, "HTTP 404")

		// The notebook is gone, so cleanup has nothing left to delete.
		uid = ""
	})
}
