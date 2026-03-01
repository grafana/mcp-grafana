//go:build integration

package tools

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ListBuckets(t *testing.T) {
	t.Run("list buckets for FluxQL linked DataSource", func(t *testing.T) {
		ctx := newTestContext()

		result, err := listBuckets(ctx, ListBucketArgs{
			DatasourceUID: "influxdb-flux",
		})
		require.NoError(t, err)

		assert.Contains(t, *result.Buckets, "b-system-logs", "should list buckets for FluxQL DataSource")
	})

	t.Run("error for SQL linked Datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := listBuckets(ctx, ListBucketArgs{
			DatasourceUID: "influxdb-sql",
		})
		require.Error(t, err, "Datasource is not configured with FluxQL , bucket listing is explicit to FluxQL linked datasources")
	})

	t.Run("error for InfluxQL linked Datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := listBuckets(ctx, ListBucketArgs{
			DatasourceUID: "influxdb-influxql",
		})
		require.Error(t, err, "Datasource is not configured with FluxQL , bucket listing is explicit to FluxQL linked datasources")
	})
}

func Test_ListMeasurements(t *testing.T) {
	t.Run("require bucket for FluxQL Datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := listMeasurements(ctx, ListMeasurementsArgs{
			DatasourceUID: "influxdb-flux",
		})
		require.Error(t, err, fmt.Sprintf("Bucket is required for %s linked InfluxDb Datasources", FluxQueryType))
	})

	t.Run("list measurements for Flux linked Datasource", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listMeasurements(ctx, ListMeasurementsArgs{
			DatasourceUID: "influxdb-flux",
			Bucket:        "b-system-logs",
		})
		require.NoError(t, err)

		t.Log(result.Measurements, result.Hints, result.MeasurementCount)
		assert.Subset(t, *result.Measurements, []string{"test"}, "should list measurements for Flux linked Datasource")
	})

	t.Run("should list measurements for SQL linked Datasoure ", func(t *testing.T) {
		ctx := newTestContext()
		result, err := listMeasurements(ctx, ListMeasurementsArgs{
			DatasourceUID: "influxdb-sql",
			Bucket:        "b-system-logs",
		})
		require.NoError(t, err)
		assert.Subset(t, *result.Measurements, []string{})
	})

	t.Run("should list buckets for InfluxQL linked Datasoure", func(t *testing.T) {})
}
