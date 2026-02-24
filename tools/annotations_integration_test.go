// Requires a Grafana instance running on localhost:3000,
// Run with `go test -tags integration`.
//go:build integration

package tools

import (
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client/annotations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	// create and update annotation.
	t.Run("create and update annotation", func(t *testing.T) {
		// 1. create annotation.
		resp, err := createAnnotation(ctx, CreateAnnotationInput{
			DashboardUID: *newUID,
			Time:         time.Now().UnixMilli(),
			Text:         "integration-test-update-initial",
			Tags:         []string{"init"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		created, ok := resp.(*annotations.PostAnnotationOK)
		require.True(t, ok)
		id := created.Payload.ID // *int64

		// 2. update annotation (PATCH semantics).
		newText := "integration-test-updated"
		newTime := time.Now().UnixMilli()
		_, err = updateAnnotation(ctx, UpdateAnnotationInput{
			ID:   *id,
			Time: &newTime,
			Text: &newText,
			Tags: []string{"updated"},
		})
		require.NoError(t, err)
	})

	// create graphite annotation via merged tool.
	t.Run("create graphite annotation", func(t *testing.T) {
		resp, err := createAnnotation(ctx, CreateAnnotationInput{
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
		limit := int64(1)
		out, err := getAnnotations(ctx, GetAnnotationsInput{
			DashboardUID: newUID,
			Limit:        &limit,
		})
		require.NoError(t, err)
		assert.NotNil(t, out)
	})

	// list all tags.
	t.Run("list annotation tags", func(t *testing.T) {
		out, err := getAnnotationTags(ctx, GetAnnotationTagsInput{})
		require.NoError(t, err)
		assert.NotNil(t, out)
	})
}
