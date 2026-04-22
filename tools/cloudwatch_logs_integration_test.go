//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudWatchLogsIntegration_ListLogGroups(t *testing.T) {
	ctx := newTestContext()

	result, err := listCloudWatchLogGroups(ctx, ListCloudWatchLogGroupsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result), 1, "Should find at least one log group")
}

func TestCloudWatchLogsIntegration_ListLogGroupFields(t *testing.T) {
	ctx := newTestContext()

	result, err := listCloudWatchLogGroupFields(ctx, ListCloudWatchLogGroupFieldsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
		LogGroupName:  "test-application-logs",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, len(result), 0)
}

func TestCloudWatchLogsIntegration_QueryLogs(t *testing.T) {
	ctx := newTestContext()

	result, err := queryCloudWatchLogs(ctx, QueryCloudWatchLogsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
		LogGroupNames: []string{"test-application-logs"},
		QueryString:   "fields @timestamp, @message | sort @timestamp desc | limit 5",
		Start:         "now-1h",
		End:           "now",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotNil(t, result.Logs)
}

func TestCloudWatchLogsIntegration_QueryEmptyResult(t *testing.T) {
	ctx := newTestContext()

	result, err := queryCloudWatchLogs(ctx, QueryCloudWatchLogsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
		LogGroupNames: []string{"nonexistent-log-group"},
		QueryString:   "fields @timestamp, @message | limit 5",
		Start:         "now-1h",
		End:           "now",
	})

	if err == nil {
		require.NotNil(t, result)
		if len(result.Logs) == 0 {
			assert.NotEmpty(t, result.Hints, "Empty result should have hints")
		}
	}
}

func TestCloudWatchLogsIntegration_InvalidDatasource(t *testing.T) {
	ctx := newTestContext()

	_, err := listCloudWatchLogGroups(ctx, ListCloudWatchLogGroupsParams{
		DatasourceUID: "nonexistent-uid",
		Region:        "us-east-1",
	})

	require.Error(t, err)
}

func TestCloudWatchLogsIntegration_WrongDatasourceType(t *testing.T) {
	ctx := newTestContext()

	_, err := listCloudWatchLogGroups(ctx, ListCloudWatchLogGroupsParams{
		DatasourceUID: "prometheus",
		Region:        "us-east-1",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not cloudwatch")
}
