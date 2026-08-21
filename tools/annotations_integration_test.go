// Requires a Grafana instance running on localhost:3000,
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client/annotations"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ptr is duplicated from annotations_unit_test.go, which isn't compiled
// under this file's "integration" build tag.
func ptr[T any](v T) *T { return &v }

func TestAnnotationTools(t *testing.T) {
	ctx := newTestContext()

	// get existing provisioned dashboard.
	orig := getExistingTestDashboard(t, ctx, "")
	origMap := getTestDashboardJSON(t, ctx, orig)

	// remove identifiers so grafana treats it as a new dashboard
	delete(origMap, "uid")
	delete(origMap, "id")
	origMap["title"] = "Integration Test for Annotations"

	// create new dashboard.
	result, err := updateDashboard(ctx, UpdateDashboardParams{
		Dashboard: origMap,
		Message:   "creating new dashboard for Annotations Tool Test",
		Overwrite: false,
		UserID:    1,
	})

	require.NoError(t, err)

	// new UID for the test dashboard.
	newUID := result.UID

	t.Cleanup(func() {
		c := mcpgrafana.GrafanaClientFromContext(ctx)
		_, _ = c.Dashboards.DeleteDashboardByUID(*newUID)
	})

	// create, update, and delete an annotation.
	t.Run("create, update, and delete annotation", func(t *testing.T) {
		// 1. create annotation.
		resp, err := createAnnotation(ctx, AnnotationsWriteParams{
			DashboardUID: *newUID,
			Time:         ptr(time.Now().UnixMilli()),
			Text:         ptr("integration-test-update-initial"),
			Tags:         []string{"init"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		created, ok := resp.(*annotations.PostAnnotationOK)
		require.True(t, ok)
		id := created.Payload.ID // *int64

		// 2. update annotation (PATCH semantics).
		_, err = updateAnnotation(ctx, AnnotationsWriteParams{
			ID:   *id,
			Time: ptr(time.Now().UnixMilli()),
			Text: ptr("integration-test-updated"),
			Tags: []string{"updated"},
		})
		require.NoError(t, err)

		// 3. delete annotation.
		msg, err := deleteAnnotation(ctx, AnnotationsWriteParams{ID: *id})
		require.NoError(t, err)
		assert.NotEmpty(t, msg)
	})

	// create graphite annotation via merged tool.
	t.Run("create graphite annotation", func(t *testing.T) {
		resp, err := createAnnotation(ctx, AnnotationsWriteParams{
			Format: "graphite",
			What:   "integration-test-graphite",
			When:   time.Now().UnixMilli(),
			Tags:   []string{"mcp", "graphite"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	// list all annotations.
	t.Run("list annotations", func(t *testing.T) {
		out, err := getAnnotations(ctx, annotationsReadRequest{
			DashboardUID: newUID,
			Limit:        ptr(int64(1)),
		})
		require.NoError(t, err)
		assert.NotNil(t, out)
	})

	// list all tags.
	t.Run("list annotation tags", func(t *testing.T) {
		out, err := getAnnotationTags(ctx, annotationsReadRequest{})
		require.NoError(t, err)
		assert.NotNil(t, out)
	})

	// The tests above exercise the individual handlers directly; this one
	// goes through the consolidated annotations_read/annotations_write
	// entrypoints end to end, to catch any wiring mistake in their dispatch
	// that per-handler tests wouldn't see.
	t.Run("annotations_write and annotations_read entrypoints", func(t *testing.T) {
		created, err := annotationsWrite(ctx, AnnotationsWriteParams{
			Operation: "create",
			Text:      ptr("integration-test-entrypoint"),
		})
		require.NoError(t, err)
		createdOK, ok := created.(*annotations.PostAnnotationOK)
		require.True(t, ok)
		id := *createdOK.Payload.ID

		_, err = annotationsWrite(ctx, AnnotationsWriteParams{
			Operation: "update",
			ID:        id,
			Text:      ptr("integration-test-entrypoint-updated"),
		})
		require.NoError(t, err)

		listed, err := annotationsRead(ctx, AnnotationsReadParams{Operation: "list"})
		require.NoError(t, err)
		assert.NotNil(t, listed)

		_, err = annotationsWrite(ctx, AnnotationsWriteParams{Operation: "delete", ID: id})
		require.NoError(t, err)
	})
}
