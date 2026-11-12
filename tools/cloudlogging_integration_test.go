//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cloudLoggingDatasourceUID = "googlecloud-logging"

func TestCloudLoggingIntegration_ClientAcceptsProvisionedDatasource(t *testing.T) {
	ctx := newTestContext()

	client, err := newCloudLoggingClient(ctx, cloudLoggingDatasourceUID)
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Equal(t, cloudLoggingDatasourceUID, client.uid)
}

func TestCloudLoggingIntegration_ClientRejectsOtherDatasourceTypes(t *testing.T) {
	ctx := newTestContext()

	_, err := newCloudLoggingClient(ctx, "loki")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is of type loki, not "+CloudLoggingDatasourceType)

	_, err = newCloudLoggingClient(ctx, "does-not-exist")
	require.Error(t, err)
}

func TestCloudLoggingIntegration_QueryWithoutCredentialsFailsCleanly(t *testing.T) {
	ctx := newTestContext()

	result, err := queryCloudLogging(ctx, CloudLoggingQueryParams{
		DatasourceUID: cloudLoggingDatasourceUID,
		ProjectID:     "integration-test-project",
		Filter:        `severity>=ERROR`,
		Limit:         5,
	})
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestCloudLoggingIntegration_DiscoveryWithoutCredentialsFailsCleanly(t *testing.T) {
	ctx := newTestContext()

	_, err := listCloudLoggingProjects(ctx, ListCloudLoggingProjectsParams{DatasourceUID: cloudLoggingDatasourceUID})
	require.Error(t, err)

	_, err = listCloudLoggingBuckets(ctx, ListCloudLoggingBucketsParams{
		DatasourceUID: cloudLoggingDatasourceUID,
		ProjectID:     "integration-test-project",
	})
	require.Error(t, err)
}
