//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tempoTestDatasourceUID = "tempo"

func TestTempoIntegration_Search(t *testing.T) {
	ctx := newTestContext()

	result, err := searchTempoTraces(ctx, SearchTempoTracesParams{
		DatasourceUID: tempoTestDatasourceUID,
		Query:         "{}",
		Start:         "now-1h",
		End:           "now",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "search should not return error: %v", result)
	assert.NotEmpty(t, result.Content, "search should return content")
}

func TestTempoIntegration_Search_InvalidQuery(t *testing.T) {
	ctx := newTestContext()

	result, err := searchTempoTraces(ctx, SearchTempoTracesParams{
		DatasourceUID: tempoTestDatasourceUID,
		Query:         "this is not valid traceql",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError, "invalid query should return tool error")
}

func TestTempoIntegration_MetricsInstant(t *testing.T) {
	ctx := newTestContext()

	result, err := queryTempoMetrics(ctx, QueryTempoMetricsParams{
		DatasourceUID: tempoTestDatasourceUID,
		Query:         "{ } | count_over_time()",
		Type:          "instant",
		Start:         "now-1h",
		End:           "now",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "metrics instant should not return error: %v", result)
}

func TestTempoIntegration_MetricsRange(t *testing.T) {
	ctx := newTestContext()

	result, err := queryTempoMetrics(ctx, QueryTempoMetricsParams{
		DatasourceUID: tempoTestDatasourceUID,
		Query:         "{ } | count_over_time()",
		Start:         "now-1h",
		End:           "now",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "metrics range should not return error: %v", result)
}

func TestTempoIntegration_GetAttributeNames(t *testing.T) {
	ctx := newTestContext()

	result, err := listTempoAttributeNames(ctx, ListTempoAttributeNamesParams{
		DatasourceUID: tempoTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "get attribute names should not return error: %v", result)
	assert.NotEmpty(t, result.Content, "should return content")
}

func TestTempoIntegration_GetAttributeNames_WithScope(t *testing.T) {
	ctx := newTestContext()

	result, err := listTempoAttributeNames(ctx, ListTempoAttributeNamesParams{
		DatasourceUID: tempoTestDatasourceUID,
		Scope:         "resource",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "get attribute names with scope should not return error: %v", result)
}

func TestTempoIntegration_GetAttributeValues(t *testing.T) {
	ctx := newTestContext()

	result, err := listTempoAttributeValues(ctx, ListTempoAttributeValuesParams{
		DatasourceUID: tempoTestDatasourceUID,
		Name:          "resource.service.name",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "get attribute values should not return error: %v", result)
}

func TestTempoIntegration_InvalidDatasource(t *testing.T) {
	ctx := newTestContext()

	result, err := searchTempoTraces(ctx, SearchTempoTracesParams{
		DatasourceUID: "nonexistent-datasource",
		Query:         "{}",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError, "should return tool error for invalid datasource")
}

func TestTempoIntegration_WrongDatasourceType(t *testing.T) {
	ctx := newTestContext()

	result, err := searchTempoTraces(ctx, SearchTempoTracesParams{
		DatasourceUID: "prometheus",
		Query:         "{}",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError, "should return tool error for non-tempo datasource")
}

func TestTempoIntegration_FractionalSeconds(t *testing.T) {
	ctx := newTestContext()

	result, err := searchTempoTraces(ctx, SearchTempoTracesParams{
		DatasourceUID: tempoTestDatasourceUID,
		Query:         "{}",
		Start:         "2025-01-01T00:00:00.123Z",
		End:           "2025-01-01T01:00:00.456Z",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "fractional seconds should be accepted: %v", result)
}

func TestTempoIntegration_RelativeTime(t *testing.T) {
	ctx := newTestContext()

	result, err := searchTempoTraces(ctx, SearchTempoTracesParams{
		DatasourceUID: tempoTestDatasourceUID,
		Query:         "{}",
		Start:         "now-24h",
		End:           "now",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "relative time should be accepted: %v", result)
}

func TestTempoIntegration_ToolResultHasMeta(t *testing.T) {
	ctx := newTestContext()

	result, err := listTempoAttributeNames(ctx, ListTempoAttributeNamesParams{
		DatasourceUID: tempoTestDatasourceUID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Meta, "result should have meta")
	assert.Equal(t, "attribute-names", result.Meta.AdditionalFields["type"])
	assert.Equal(t, "json", result.Meta.AdditionalFields["encoding"])
}
