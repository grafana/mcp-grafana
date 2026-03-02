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
		require.Error(t, err, "Datasource is not configured with FluxQL, bucket listing is explicit to FluxQL linked datasources")
	})

	t.Run("error for InfluxQL linked Datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := listBuckets(ctx, ListBucketArgs{
			DatasourceUID: "influxdb-influxql",
		})
		require.Error(t, err, "Datasource is not configured with FluxQL, bucket listing is explicit to FluxQL linked datasources")
	})
}
func Test_Query(t *testing.T) {

	t.Run("Flux Query", func(t *testing.T) {
		ctx := newTestContext()

		query := `
		from(bucket: "b-system-logs")
			|> range(start: v.timeRangeStart, stop: v.timeRangeStop)
			|> filter(fn: (r) => r["_measurement"] == "auth_events")
			|> filter(fn: (r) => r["_field"] == "severity")
			|> aggregateWindow(every: v.windowPeriod, column: "_value", fn: mean, createEmpty: false)
		`

		result, err := queryInflux(ctx, InfluxQueryArgs{
			DatasourceUID: "influxdb-flux",
			Query:         query,
			QueryType:     FluxQueryType,
			Start:         "now-24h",
			End:           "now",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Frames)

		t.Log(result.Frames[0], result.Hints)

		assert.Equal(t, 10, len(result.Frames), "should contain frames of all groups")

		for _, frame := range result.Frames {
			assert.Equal(t, 1, len(frame.Rows), "should contain non-empty results for a frame")
			_, ok := frame.Rows[0]["severity"]
			// should contain field 'severity'
			assert.True(t, ok, "should contain queried fields")
		}
	})

	t.Run("SQL Query", func(t *testing.T) {
		ctx := newTestContext()
		query := `SELECT MAX("attempt_count") AS count FROM "auth_events";`

		result, err := queryInflux(ctx, InfluxQueryArgs{
			DatasourceUID: "influxdb-sql",
			Query:         query,
			QueryType:     SQLQueryType,
			Start:         "now-24h",
			End:           "now",
		})

		require.NoError(t, err)

		assert.NotEmpty(t, result.Frames, "should contain a frame")
		assert.Len(t, result.Frames, result.FramesCount, "should specify frame count equal to len(frames)")

		t.Log(result.Frames[0], result.Hints)
		assert.NotEmpty(t, result.Frames[0].Rows, "should contain results")

		attemptCount, ok := result.Frames[0].Rows[0]["count"].(float64)

		require.True(t, ok, "should contain queried columns with expected type in a row")
		assert.Equal(t, 20.0, attemptCount)
	})

	t.Run("InfluxQL Query", func(t *testing.T) {
		ctx := newTestContext()

		query := `SELECT mean("severity") FROM "auth_events" GROUP BY time($__interval) fill(null)`

		result, err := queryInflux(ctx, InfluxQueryArgs{
			DatasourceUID: "influxdb-influxql",
			Query:         query,
			QueryType:     InfluxQLQueryType,
			Start:         "now-24h",
		})
		require.NoError(t, err)
		assert.NotEmpty(t, result.Frames)

		t.Log(result.Frames[0], result.Hints)
		assert.GreaterOrEqual(t, len(result.Frames[0].Rows), 20, "should contain query results")

		t.Log(result.Frames[0].Rows[0], result.Frames[0].Columns)
		_, ok := result.Frames[0].Rows[0][`auth_events.mean`].(float64)

		require.True(t, ok, "should contain queried columns with expected type in a row")
	})
}
func Test_ListMeasurements(t *testing.T) {
	t.Run("require bucket for FluxQL Datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := listMeasurements(ctx, ListMeasurementsArgs{
			DatasourceUID: "influxdb-flux",
		})
		require.Error(t, err, fmt.Sprintf("Bucket is required for %s linked InfluxDB Datasources", FluxQueryType))
	})

	t.Run("bucket optional for SQL/InfluxQL Datasource", func(t *testing.T) {
		dataSourceUIDs := []string{"influxdb-sql", "influxdb-influxql"}
		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()
			_, err := listMeasurements(ctx, ListMeasurementsArgs{
				DatasourceUID: uid,
			})
			require.NoError(t, err)
		}
	})

	t.Run("list measurements of a Datasource", func(t *testing.T) {
		ctx := newTestContext()

		dataSourceUIDs := []string{"influxdb-flux", "influxdb-sql", "influxdb-influxql"}

		for _, uid := range dataSourceUIDs {
			result, err := listMeasurements(ctx, ListMeasurementsArgs{
				DatasourceUID: uid,
				Bucket:        "b-system-logs",
			})
			require.NoError(t, err)

			t.Log(result.Measurements, result.Hints, result.MeasurementCount)
			assert.Subset(t, *result.Measurements,
				[]string{"auth_events", "db_queries", "http_requests", "queue_stats", "resource_usage", "syslog"},
				"should list measurements for %s linked Datasource", uid)
		}
	})

}
func Test_ListTagKeys(t *testing.T) {

	t.Run("require bucket for FluxQL Datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := listTagKeys(ctx, ListTagKeysArgs{
			DatasourceUID: "influxdb-flux",
			Measurement:   "auth_events",
		})
		require.Error(t, err, fmt.Sprintf("Bucket is required for %s linked InfluxDb Datasources", FluxQueryType))
	})

	t.Run("list tags keys", func(t *testing.T) {
		dataSourceUIDs := []string{"influxdb-flux", "influxdb-sql", "influxdb-influxql"}

		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()

			bucket := ""

			if uid == "influxdb-flux" {
				bucket = "b-system-logs"
			}

			result, err := listTagKeys(ctx, ListTagKeysArgs{
				DatasourceUID: uid,
				Bucket:        bucket,
				Measurement:   "auth_events",
			})
			require.NoError(t, err)

			t.Log(result.TagKeys, uid, result.Hints)

			assert.Subset(t, *result.TagKeys,
				[]string{"ip", "status", "service"},
				"should list tag keys for %s linked Datasource", uid)
		}
	})

	t.Run("hints for empty results", func(t *testing.T) {
		dataSourceUIDs := []string{"influxdb-sql", "influxdb-influxql"}

		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()

			result, err := listTagKeys(ctx, ListTagKeysArgs{
				DatasourceUID: uid,
				Measurement:   "nonexistent",
			})
			require.NoError(t, err)

			t.Log(result.TagKeys, uid, result.Hints)

			assert.NotNil(t, result.Hints, "should return hints")

			assert.Empty(t, *result.TagKeys, "should return empty list for non-existent measurement")
		}
	})

}
func Test_ListFieldKeys(t *testing.T) {

	t.Run("require bucket for FluxQL Datasource", func(t *testing.T) {
		ctx := newTestContext()
		_, err := listFieldKeys(ctx, ListFieldKeysArgs{
			DatasourceUID: "influxdb-flux",
			Measurement:   "auth_events",
		})
		require.Error(t, err, fmt.Sprintf("Bucket is required for %s linked InfluxDB Datasources", FluxQueryType))
	})

	t.Run("list field keys", func(t *testing.T) {
		dataSourceUIDs := []string{"influxdb-flux", "influxdb-sql", "influxdb-influxql"}

		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()

			bucket := ""

			if uid == "influxdb-flux" {
				bucket = "b-system-logs"
			}

			result, err := listFieldKeys(ctx, ListFieldKeysArgs{
				DatasourceUID: uid,
				Bucket:        bucket,
				Measurement:   "auth_events",
			})
			require.NoError(t, err)

			t.Log(result.FieldKeys, uid, result.Hints)

			assert.Subset(t, *result.FieldKeys,
				[]string{"attempt_count", "severity"},
				"should list field keys for %s linked Datasource", uid)
		}
	})

	t.Run("hints for empty results", func(t *testing.T) {
		dataSourceUIDs := []string{"influxdb-sql", "influxdb-influxql"}

		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()

			result, err := listFieldKeys(ctx, ListFieldKeysArgs{
				DatasourceUID: uid,
				Measurement:   "nonexistent",
			})
			require.NoError(t, err)

			t.Log(result.FieldKeys, uid, result.Hints)

			assert.NotNil(t, result.Hints, "should return hints")

			assert.Empty(t, *result.FieldKeys, "should return empty list for non-existent measurement")
		}
	})

}
func Test_Limit(t *testing.T) {
	dataSourceUIDs := []string{"influxdb-flux", "influxdb-sql", "influxdb-influxql"}

	t.Run("list measurements with limits ", func(t *testing.T) {

		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()

			bucket := ""
			if uid == "influxdb-flux" {
				bucket = "b-system-logs"
			}

			result, err := listMeasurements(ctx, ListMeasurementsArgs{
				DatasourceUID: uid,
				Bucket:        bucket,
				Limit:         1,
			})
			require.NoError(t, err)

			t.Log(result.Measurements, uid, result.Hints)

			assert.Len(t, *result.Measurements, 1)
		}
	})

	t.Run("list tag keys with limit", func(t *testing.T) {
		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()

			bucket := ""

			if uid == "influxdb-flux" {
				bucket = "b-system-logs"
			}

			result, err := listTagKeys(ctx, ListTagKeysArgs{
				DatasourceUID: uid,
				Bucket:        bucket,
				Measurement:   "auth_events",
				Limit:         1,
			})
			require.NoError(t, err)

			t.Log(result.TagKeys, uid, result.Hints)

			assert.Len(t, *result.TagKeys, 1)
		}
	})

	t.Run("list field keys with limit", func(t *testing.T) {
		for _, uid := range dataSourceUIDs {
			ctx := newTestContext()

			bucket := ""

			if uid == "influxdb-flux" {
				bucket = "b-system-logs"
			}

			result, err := listFieldKeys(ctx, ListFieldKeysArgs{
				DatasourceUID: uid,
				Bucket:        bucket,
				Measurement:   "auth_events",
				Limit:         1,
			})
			require.NoError(t, err)

			t.Log(result.FieldKeys, uid, result.Hints)

			assert.Len(t, *result.FieldKeys, 1)
		}
	})

	t.Run("execute query with limits", func(t *testing.T) {

		queries := []*InfluxQueryArgs{
			{
				Query: `
				import "generate"
					t1 =
						generate.from(
							count: 4,
							fn: (n) => n + 1,
							start: 2022-01-01T00:00:00Z,
							stop: 2022-01-05T00:00:00Z,
						)
							|> set(key: "tag", value: "foo")
							|> group(columns: ["tag"])

					t2 =
						generate.from(
							count: 4,
							fn: (n) => n * (-1),
							start: 2022-01-01T00:00:00Z,
							stop: 2022-01-05T00:00:00Z,
						)
							|> set(key: "tag", value: "bar")
							|> group(columns: ["tag"])

					union(tables: [t1, t2])
				`,
				DatasourceUID: "influxdb-flux",
				QueryType:     FluxQueryType,
			},
			{
				Query:         `SELECT "attempt_count" FROM "auth_events" LIMIT 3;`,
				DatasourceUID: "influxdb-sql",
				QueryType:     SQLQueryType,
			},
			{
				Query:         `SELECT attempt_count FROM "auth_events" fill(null) LIMIT 10 OFFSET 2`,
				DatasourceUID: "influxdb-influxql",
				QueryType:     InfluxQLQueryType,
			},
		}
		limit := 1
		for _, query := range queries {
			ctx := newTestContext()

			query.Start = "now-4h"
			query.End = "now"
			query.Limit = uint(limit)

			result, err := queryInflux(ctx, *query)

			require.NoError(t, err)
			t.Log(result.Frames, result.Hints)
			assert.GreaterOrEqual(t, len(result.Frames), 1)
			for _, frame := range result.Frames {
				assert.Equal(t, limit, len(frame.Rows), "should limit number of rows of a frame")
			}
		}
	})
}
