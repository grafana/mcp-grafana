//go:build cloud

// Cloud integration tests for CloudWatch Logs Insights. These run against the
// datasourcedev stack (DATASOURCEDEV_GRAFANA_URL,
// DATASOURCEDEV_GRAFANA_SERVICE_ACCOUNT_TOKEN), whose CloudWatch datasource
// authenticates as a monitoring account (569069006612) linked to a source
// account (366620023056) in us-east-1. The stack rejects query ranges over 1d.
// Tests skip if the environment variables are not set.

package tools

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// cloudWatchCloudDatasourceUID uses Grafana Assume Role, which can detect
	// the monitoring account; cloudwatch-ds-m can't (its IAM user lacks oam:ListSinks).
	cloudWatchCloudDatasourceUID = "efth74i1szn5sc"
	cloudWatchCloudRegion        = "us-east-1"
	// cloudWatchCloudLogGroup is a busy log group in the monitoring account.
	cloudWatchCloudLogGroup = "/aws/eks/app-signals-demo/cluster"
	// cloudWatchCloudSourceAccount and cloudWatchCloudSourceLogGroupARN identify a
	// busy log group that only exists in the linked source account.
	cloudWatchCloudSourceAccount     = "366620023056"
	cloudWatchCloudSourceLogGroupARN = "arn:aws:logs:us-east-1:366620023056:log-group:aws-cloudtrail-logs-bookstore"
)

func createCloudWatchCloudTestContext(t *testing.T) context.Context {
	t.Helper()
	if os.Getenv("DATASOURCEDEV_GRAFANA_URL") == "" {
		t.Skip("DATASOURCEDEV_GRAFANA_URL not set, skipping CloudWatch cloud tests")
	}
	return createCloudTestContext(t, "CloudWatch", "DATASOURCEDEV_GRAFANA_URL", "DATASOURCEDEV_GRAFANA_API_KEY")
}

func TestCloudWatchLogsCloud_ListLogGroups(t *testing.T) {
	ctx := createCloudWatchCloudTestContext(t)

	t.Run("monitoring account names", func(t *testing.T) {
		result, err := listCloudWatchLogGroups(ctx, ListCloudWatchLogGroupsParams{
			DatasourceUID: cloudWatchCloudDatasourceUID,
			Region:        cloudWatchCloudRegion,
			Prefix:        "/aws/eks/app-signals",
		})
		skipOnDatasourceAuthError(t, err)
		require.NoError(t, err)
		assert.Contains(t, result, cloudWatchCloudLogGroup)
	})

	t.Run("source account ARNs", func(t *testing.T) {
		result, err := listCloudWatchLogGroups(ctx, ListCloudWatchLogGroupsParams{
			DatasourceUID: cloudWatchCloudDatasourceUID,
			Region:        cloudWatchCloudRegion,
			AccountId:     cloudWatchCloudSourceAccount,
			// The source account has more than the 50 groups a listing returns.
			Prefix: "aws-cloudtrail",
		})
		skipOnDatasourceAuthError(t, err)
		require.NoError(t, err)
		assert.Contains(t, result, cloudWatchCloudSourceLogGroupARN)
	})
}

func TestCloudWatchLogsCloud_ListLogGroupFields(t *testing.T) {
	ctx := createCloudWatchCloudTestContext(t)

	result, err := listCloudWatchLogGroupFields(ctx, ListCloudWatchLogGroupFieldsParams{
		DatasourceUID: cloudWatchCloudDatasourceUID,
		Region:        cloudWatchCloudRegion,
		LogGroupName:  cloudWatchCloudLogGroup,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	assert.Contains(t, result, "@message")
}

func TestCloudWatchLogsCloud_QueryReturnsNewestRows(t *testing.T) {
	ctx := createCloudWatchCloudTestContext(t)

	// Fixed bounds, settled for ingestion, so both queries see the same events.
	end := time.Now().Add(-15 * time.Minute).UTC().Truncate(time.Second)
	query := func(queryString string) *CloudWatchLogsQueryResult {
		t.Helper()
		result, err := queryCloudWatchLogs(ctx, QueryCloudWatchLogsParams{
			DatasourceUID: cloudWatchCloudDatasourceUID,
			Region:        cloudWatchCloudRegion,
			LogGroups:     []string{cloudWatchCloudLogGroup},
			QueryString:   queryString,
			Start:         end.Add(-30 * time.Minute).Format(time.RFC3339),
			End:           end.Format(time.RFC3339),
			Limit:         3,
		})
		skipOnDatasourceAuthError(t, err)
		require.NoError(t, err)
		require.Equal(t, "Complete", result.Status)
		require.Len(t, result.Logs, 3)
		return result
	}
	newest := func(r *CloudWatchLogsQueryResult) string {
		var ts []string
		for _, row := range r.Logs {
			ts = append(ts, row["@timestamp"])
		}
		return slices.Max(ts)
	}

	// A query limit above the tool limit must still yield the newest rows,
	// even though Grafana re-sorts result rows by ascending time.
	wide := query("fields @timestamp, @message | sort @timestamp desc | limit 20")
	exact := query("fields @timestamp, @message | sort @timestamp desc | limit 3")
	assert.Equal(t, newest(exact), newest(wide))
	assert.GreaterOrEqual(t, wide.TotalFound, 3)
}

func TestCloudWatchLogsCloud_QuerySourceAccountByARN(t *testing.T) {
	ctx := createCloudWatchCloudTestContext(t)

	result, err := queryCloudWatchLogs(ctx, QueryCloudWatchLogsParams{
		DatasourceUID: cloudWatchCloudDatasourceUID,
		Region:        cloudWatchCloudRegion,
		LogGroups:     []string{cloudWatchCloudSourceLogGroupARN},
		QueryString:   "fields @timestamp, @message | sort @timestamp desc",
		Start:         "now-1h",
		Limit:         5,
	})
	skipOnDatasourceAuthError(t, err)
	require.NoError(t, err)
	assert.Equal(t, "Complete", result.Status)
	assert.NotEmpty(t, result.Logs)
}
