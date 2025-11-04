//go:build cloud
// +build cloud

// This file contains cloud integration tests for the annotation tools.
// The tests run against a real Grafana instance specified by:
//   - GRAFANA_URL
//   - GRAFANA_SERVICE_ACCOUNT_TOKEN  (preferred)
//   - GRAFANA_API_KEY               (deprecated)
//
// Tests will be skipped automatically if the required environment variables
// are not set. These tests verify end-to-end behavior of annotation tooling
// against a live Grafana server (no mocks).

package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudAnnotationTools(t *testing.T) {
	ctx := createCloudTestContext(t, "Annotations", "GRAFANA_URL", "GRAFANA_API_KEY")

	// create, update and patch
	t.Run("create, update and patch annotation", func(t *testing.T) {
		// 1. create annotation
		created, err := createAnnotation(ctx, CreateAnnotationInput{
			Time: time.Now().UnixMilli(),
			Text: "integration-test-update-initial",
			Tags: []string{"init"},
		})
		require.NoError(t, err)
		require.NotNil(t, created)

		id := created.Payload.ID // *int64

		// 2. update annotation (PUT)
		_, err = updateAnnotation(ctx, UpdateAnnotationInput{
			ID:   *id,
			Time: time.Now().UnixMilli(),
			Text: "integration-test-updated",
			Tags: []string{"updated"},
		})
		require.NoError(t, err)

		// 3. patch annotation (PATCH)
		newText := "patched"
		_, err = patchAnnotation(ctx, PatchAnnotationInput{
			ID:   *id,
			Text: &newText,
		})
		require.NoError(t, err)
	})

	// create graphite annotation
	t.Run("create graphite annotation", func(t *testing.T) {
		resp, err := createAnnotationGraphiteFormat(ctx, CreateGraphiteAnnotationInput{
			What: "integration-test-graphite",
			When: time.Now().UnixMilli(),
			Tags: []string{"mcp", "graphite"},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	// list all annotations
	t.Run("list annotations", func(t *testing.T) {
		limit := int64(1)
		out, err := getAnnotations(ctx, GetAnnotationsInput{Limit: &limit})
		require.NoError(t, err)
		assert.NotNil(t, out)
	})

	// list all tags
	t.Run("list annotation tags", func(t *testing.T) {
		out, err := getAnnotationTags(ctx, GetAnnotationTagsInput{})
		require.NoError(t, err)
		assert.NotNil(t, out)
	})
}
