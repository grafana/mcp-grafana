//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const azureMonitorDatasourceUID = "azure-monitor"

func TestAzureMonitorIntegration_ClientAcceptsProvisionedDatasource(t *testing.T) {
	ctx := newTestContext()

	client, baseURL, err := newAzureMonitorClient(ctx, azureMonitorDatasourceUID)
	require.NoError(t, err)
	assert.NotNil(t, client)
	assert.NotEmpty(t, baseURL)
}

func TestAzureMonitorIntegration_ClientRejectsOtherDatasourceTypes(t *testing.T) {
	ctx := newTestContext()

	_, _, err := newAzureMonitorClient(ctx, "loki")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is of type loki, not "+AzureMonitorDatasourceType)

	_, _, err = newAzureMonitorClient(ctx, "does-not-exist")
	require.Error(t, err)
}

func TestAzureMonitorIntegration_QueryWithoutCredentialsFailsCleanly(t *testing.T) {
	ctx := newTestContext()

	result, err := queryAzureLogAnalytics(ctx, AzureLogAnalyticsQueryParams{
		DatasourceUID: azureMonitorDatasourceUID,
		Query:         "AppRequests",
		Resources:     []string{"/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/law"},
		Limit:         5,
	})
	require.Error(t, err)
	assert.Nil(t, result)

	graph, err := queryAzureResourceGraph(ctx, AzureResourceGraphQueryParams{
		DatasourceUID: azureMonitorDatasourceUID,
		Query:         "resources",
		Limit:         5,
	})
	require.Error(t, err)
	assert.Nil(t, graph)
}
