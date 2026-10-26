//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// list_cloudwatch_log_group_fields has no integration test: LocalStack does
// not implement GetLogGroupFields.

func TestCloudWatchLogsIntegration_ListLogGroups(t *testing.T) {
	ctx := newTestContext()

	result, err := listCloudWatchLogGroups(ctx, ListCloudWatchLogGroupsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
		Prefix:        "test-",
	})
	require.NoError(t, err)
	assert.Contains(t, result, "test-application-logs")

	result, err = listCloudWatchLogGroups(ctx, ListCloudWatchLogGroupsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
		Prefix:        "no-such-prefix-",
	})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestCloudWatchLogsIntegration_QueryLogs(t *testing.T) {
	ctx := newTestContext()

	result, err := queryCloudWatchLogs(ctx, QueryCloudWatchLogsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
		LogGroupNames: []string{"test-application-logs"},
		QueryString:   "fields @timestamp, @message | sort @timestamp desc | limit 5",
		// Events are seeded when LocalStack starts, which may be well before the test runs.
		Start: "now-7d",
		Limit: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, "Complete", result.Status)
	// LocalStack's Logs Insights emulation ignores filters and doesn't order
	// sorts like AWS, so only the row count is checked here.
	require.Len(t, result.Logs, 3)
	assert.NotEmpty(t, result.Logs[0]["@message"])
	assert.NotEmpty(t, result.Logs[0]["@timestamp"])
}

func TestCloudWatchLogsIntegration_QueryMissingLogGroup(t *testing.T) {
	ctx := newTestContext()

	_, err := queryCloudWatchLogs(ctx, QueryCloudWatchLogsParams{
		DatasourceUID: cloudwatchTestDatasourceUID,
		Region:        "us-east-1",
		LogGroupNames: []string{"nonexistent-log-group"},
		QueryString:   "fields @message",
		Start:         "now-1h",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ResourceNotFoundException")
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
