//go:build integration

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	influxDBFluxDatasourceUID     = "influxdb-flux"
	influxDBInfluxQLDatasourceUID = "influxdb-influxql"
)

func TestInfluxDBIntegration_Flux(t *testing.T) {
	ctx := newTestContext()

	result, err := queryInfluxDB(ctx, InfluxDBQueryParams{
		DatasourceUID: influxDBFluxDatasourceUID,
		Query: `from(bucket: "metrics")
  |> range(start: -2h)
  |> filter(fn: (r) => r._measurement == "cpu")
  |> limit(n: 5)`,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, InfluxDBDialectFlux, result.Dialect)
	assert.NotEmpty(t, result.Rows, "expected seeded cpu points to come back")
	assert.NotEmpty(t, result.Columns, "expected flux frames to carry column names")
}

func TestInfluxDBIntegration_InfluxQL(t *testing.T) {
	ctx := newTestContext()

	result, err := queryInfluxDB(ctx, InfluxDBQueryParams{
		DatasourceUID: influxDBInfluxQLDatasourceUID,
		Query:         `SELECT "usage" FROM "cpu" WHERE time > now() - 2h LIMIT 5`,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, InfluxDBDialectInfluxQL, result.Dialect)
	assert.NotEmpty(t, result.Rows, "expected seeded cpu points to come back")
}

func TestInfluxDBIntegration_ExplicitDialectOverridesInference(t *testing.T) {
	ctx := newTestContext()

	// The datasource is configured for Flux, but explicitly passing influxql
	// should be rejected by Grafana (since InfluxDB v2 + Flux config can't
	// run InfluxQL directly). Assert we get a clean error path.
	_, err := queryInfluxDB(ctx, InfluxDBQueryParams{
		DatasourceUID: influxDBFluxDatasourceUID,
		Dialect:       "influxql",
		Query:         "SELECT * FROM cpu",
	})
	require.Error(t, err)
}

func TestInfluxDBIntegration_WrongDatasourceType(t *testing.T) {
	ctx := newTestContext()

	_, err := queryInfluxDB(ctx, InfluxDBQueryParams{
		DatasourceUID: "prometheus",
		Query:         "from(bucket: \"metrics\") |> range(start: -1h)",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not influxdb")
}
