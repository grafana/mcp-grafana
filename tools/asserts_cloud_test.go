//go:build cloud
// +build cloud

// This file contains cloud integration tests that run against a dedicated test instance
// connected to a Grafana instance at (ASSERTS_GRAFANA_URL, ASSERTS_GRAFANA_SERVICE_ACCOUNT_TOKEN or ASSERTS_GRAFANA_API_KEY).
// These tests expect this configuration to exist and will skip if the required
// environment variables are not set. The ASSERTS_GRAFANA_API_KEY variable is deprecated.

package tools

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	assertsTestEntityType = "Service"
	assertsTestEntityName = "model-builder"
	assertsTestEnv        = "dev-us-central-0"
	assertsTestNamespace  = "asserts"
)

func TestAssertsCloudIntegration(t *testing.T) {
	ctx := createCloudTestContext(t, "Asserts", "ASSERTS_GRAFANA_URL", "ASSERTS_GRAFANA_API_KEY")

	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)

	t.Run("get assertions", func(t *testing.T) {
		result, err := getAssertions(ctx, GetAssertionsParams{
			StartTime:  startTime,
			EndTime:    endTime,
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "summaries")
	})

	t.Run("get graph schema", func(t *testing.T) {
		result, err := getGraphSchema(ctx, GetGraphSchemaParams{})
		require.NoError(t, err)
		assert.NotEmpty(t, result)
		assert.Contains(t, result, "entityTypes")

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))

		types, ok := parsed["entityTypes"].([]any)
		require.True(t, ok, "entityTypes should be an array")
		assert.GreaterOrEqual(t, len(types), 1, "should have at least one entity type")

		first := types[0].(map[string]any)
		assert.NotEmpty(t, first["type"], "entity type should have a type field")
		assert.NotNil(t, first["connectedTypes"], "entity type should have connectedTypes")
	})

	t.Run("get graph schema is cached", func(t *testing.T) {
		result1, err := getGraphSchema(ctx, GetGraphSchemaParams{})
		require.NoError(t, err)

		result2, err := getGraphSchema(ctx, GetGraphSchemaParams{})
		require.NoError(t, err)

		assert.Equal(t, result1, result2, "cached result should match")
	})

	t.Run("search entities", func(t *testing.T) {
		result, err := searchEntities(ctx, SearchEntitiesParams{
			EntityType: assertsTestEntityType,
			SearchText: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
			StartTime:  startTime,
			EndTime:    endTime,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))

		entities, ok := parsed["entities"].([]any)
		require.True(t, ok, "result should have entities array")
		assert.GreaterOrEqual(t, len(entities), 1, "should find at least one entity")

		first := entities[0].(map[string]any)
		assert.Equal(t, assertsTestEntityType, first["type"])
		assert.NotEmpty(t, first["name"])
	})

	t.Run("get entity slim", func(t *testing.T) {
		result, err := getEntity(ctx, GetEntityParams{
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		var parsed slimEntity
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))

		assert.Equal(t, assertsTestEntityType, parsed.Type)
		assert.Equal(t, assertsTestEntityName, parsed.Name)
		assert.NotEmpty(t, parsed.ID)
		assert.Nil(t, parsed.Properties, "slim output should not include properties")
	})

	t.Run("get entity detailed", func(t *testing.T) {
		result, err := getEntity(ctx, GetEntityParams{
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
			Detailed:   true,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		var parsed graphEntityResponse
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))

		assert.Equal(t, assertsTestEntityType, parsed.Type)
		assert.Equal(t, assertsTestEntityName, parsed.Name)
		assert.Greater(t, parsed.ID, int64(0), "detailed output should have numeric ID")
	})

	t.Run("get connected entities", func(t *testing.T) {
		result, err := getConnectedEntities(ctx, GetConnectedEntitiesParams{
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
			Limit:      5,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))

		source, ok := parsed["source"].(map[string]any)
		require.True(t, ok, "result should have source")
		assert.Equal(t, assertsTestEntityName, source["name"])

		connected, ok := parsed["connected"].([]any)
		require.True(t, ok, "result should have connected array")
		assert.GreaterOrEqual(t, len(connected), 0, "connected may be empty but should be present")
	})

	t.Run("search entities list mode", func(t *testing.T) {
		result, err := searchEntities(ctx, SearchEntitiesParams{
			Mode:       "list",
			EntityType: assertsTestEntityType,
			Limit:      5,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))

		assert.Equal(t, "list", parsed["mode"])

		entities, ok := parsed["entities"].([]any)
		require.True(t, ok, "result should have entities array")
		assert.GreaterOrEqual(t, len(entities), 1, "should list at least one entity")

		assert.NotNil(t, parsed["pagination"], "result should include pagination")
	})

	t.Run("search entities count mode", func(t *testing.T) {
		result, err := searchEntities(ctx, SearchEntitiesParams{
			Mode:      "count",
			Env:       assertsTestEnv,
			StartTime: startTime,
			EndTime:   endTime,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))
		assert.GreaterOrEqual(t, len(parsed), 1, "should return at least one entity type count")
	})

	t.Run("get assertion summary", func(t *testing.T) {
		result, err := getAssertionSummary(ctx, GetAssertionSummaryParams{
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
			StartTime:  startTime,
			EndTime:    endTime,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})

	t.Run("search rca patterns", func(t *testing.T) {
		result, err := searchRcaPatterns(ctx, SearchRcaPatternsParams{
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
			StartTime:  startTime,
			EndTime:    endTime,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})

	t.Run("search entities semantic mode fallback", func(t *testing.T) {
		result, err := searchEntities(ctx, SearchEntitiesParams{
			Mode:       "semantic",
			SearchText: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
			Limit:      5,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(result), &parsed))

		assert.Equal(t, "deterministic_fallback", parsed["mode"],
			"without GRAFANA_SEARCH_SERVICE_URL, should fall back to deterministic search")

		results, ok := parsed["results"].([]any)
		require.True(t, ok, "result should have results array")
		assert.GreaterOrEqual(t, len(results), 1, "fallback should find at least one entity matching the name")
	})

	t.Run("entity info cache eliminates redundant calls", func(t *testing.T) {
		result1, err := getEntity(ctx, GetEntityParams{
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
		})
		require.NoError(t, err)

		result2, err := getEntity(ctx, GetEntityParams{
			EntityType: assertsTestEntityType,
			EntityName: assertsTestEntityName,
			Env:        assertsTestEnv,
			Namespace:  assertsTestNamespace,
		})
		require.NoError(t, err)

		assert.Equal(t, result1, result2, "cached entity info should return identical results")
	})
}
