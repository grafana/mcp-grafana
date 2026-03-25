//go:build unit

package tools

import (
	"errors"
	"testing"
	"time"

	"github.com/grafana/mcp-grafana/pkg/grafana"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_enforceFieldKeysLimit(t *testing.T) {
	t.Run("test_feature", func(t *testing.T) {
		t.Run("should apply maximum limit when exceeded", func(t *testing.T) {
			args := ListFieldKeysArgs{Limit: InfluxDBTagsMaxLimit + 10}
			enforceFieldKeysLimit(&args)
			assert.Equal(t, InfluxDBTagsMaxLimit, args.Limit, "limit should be maximum limit")
			t.Log("applied max limit")
		})

		t.Run("should apply default limit when limit is 0", func(t *testing.T) {
			args := ListFieldKeysArgs{Limit: 0}
			enforceFieldKeysLimit(&args)
			assert.Equal(t, InfluxDBTagsDefaultLimit, args.Limit, "limit should be default limit")
			t.Log("applied default limit")
		})

		t.Run("should keep custom limit when within bounds", func(t *testing.T) {
			args := ListFieldKeysArgs{Limit: 50}
			enforceFieldKeysLimit(&args)
			assert.Equal(t, uint(50), args.Limit, "limit should remain custom")
			t.Log("kept custom limit")
		})
	})
}

func Test_enforceTagKeysLimit(t *testing.T) {
	t.Run("test_feature", func(t *testing.T) {
		t.Run("should apply maximum limit when exceeded", func(t *testing.T) {
			args := ListTagKeysArgs{Limit: InfluxDBTagsMaxLimit + 10}
			enforceTagKeysLimit(&args)
			assert.Equal(t, InfluxDBTagsMaxLimit, args.Limit, "limit should be maximum limit")
			t.Log("applied max limit")
		})

		t.Run("should apply default limit when limit is 0", func(t *testing.T) {
			args := ListTagKeysArgs{Limit: 0}
			enforceTagKeysLimit(&args)
			assert.Equal(t, InfluxDBTagsDefaultLimit, args.Limit, "limit should be default limit")
			t.Log("applied default limit")
		})

		t.Run("should keep custom limit when within bounds", func(t *testing.T) {
			args := ListTagKeysArgs{Limit: 80}
			enforceTagKeysLimit(&args)
			assert.Equal(t, uint(80), args.Limit, "limit should remain custom")
			t.Log("kept custom limit")
		})
	})
}

func Test_enforceMeasurementsLimit(t *testing.T) {
	t.Run("test_feature", func(t *testing.T) {
		t.Run("should apply maximum limit when exceeded", func(t *testing.T) {
			args := ListMeasurementsArgs{Limit: InfluxDBMeasurementsMaxLimit + 100}
			enforceMeasurementsLimit(&args)
			assert.Equal(t, InfluxDBMeasurementsMaxLimit, args.Limit, "limit should be maximum limit")
			t.Log("applied max limit")
		})

		t.Run("should apply default limit when limit is 0", func(t *testing.T) {
			args := ListMeasurementsArgs{Limit: 0}
			enforceMeasurementsLimit(&args)
			assert.Equal(t, InfluxDBMeasurementsDefaultLimit, args.Limit, "limit should be default limit")
			t.Log("applied default limit")
		})

		t.Run("should keep custom limit when within bounds", func(t *testing.T) {
			args := ListMeasurementsArgs{Limit: 120}
			enforceMeasurementsLimit(&args)
			assert.Equal(t, uint(120), args.Limit, "limit should remain custom")
			t.Log("kept custom limit")
		})
	})
}

func Test_enforceQueryLimit(t *testing.T) {
	t.Run("test_feature", func(t *testing.T) {
		t.Run("should wrap sql query and apply limit", func(t *testing.T) {
			args := InfluxQueryArgs{QueryType: SQLQueryType, Query: "SELECT * FROM my_table;", Limit: 10}
			enforceQueryLimit(&args)
			assert.Equal(t, "(SELECT * FROM my_table) LIMIT 10", args.Query, "sql query should be wrapped and limited")
			t.Log("applied sql limit")
		})

		t.Run("should apply flux limit", func(t *testing.T) {
			args := InfluxQueryArgs{QueryType: FluxQueryType, Query: "from(bucket: \"my-bucket\")", Limit: 20}
			enforceQueryLimit(&args)
			assert.Equal(t, "from(bucket: \"my-bucket\")\n|>limit(n:20)", args.Query, "flux query should be limited")
			t.Log("applied flux limit")
		})

		t.Run("should replace influxql limit if exists", func(t *testing.T) {
			args := InfluxQueryArgs{QueryType: InfluxQLQueryType, Query: "SELECT * FROM my_table LIMIT 100", Limit: 50}
			enforceQueryLimit(&args)
			assert.Equal(t, "SELECT * FROM my_table LIMIT 50", args.Query, "influxql limit should be replaced")
			t.Log("applied influxql replaced limit")
		})

		t.Run("should apply default limit when no limit passed", func(t *testing.T) {
			args := InfluxQueryArgs{QueryType: SQLQueryType, Query: "SELECT * FROM table", Limit: 0}
			enforceQueryLimit(&args)
			assert.Equal(t, "(SELECT * FROM table) LIMIT 100", args.Query, "sql query should use default limit")
			t.Log("applied default sql limit")
		})
	})
}

func Test_parseTimeRange(t *testing.T) {
	t.Run("test_feature", func(t *testing.T) {
		t.Run("should parse start and infer default end", func(t *testing.T) {
			from, to, err := parseTimeRange("2026-02-02T19:00:00Z", "")
			require.NoError(t, err, "should not have error")
			assert.NotNil(t, from, "from time should not be nil")
			assert.NotNil(t, to, "to time should not be nil")
			expectedFrom, _ := time.Parse(time.RFC3339, "2026-02-02T19:00:00Z")
			assert.Equal(t, expectedFrom, *from, "from time should match parsed start")
			assert.Equal(t, expectedFrom.Add(time.Hour), *to, "to time should be 1 hour after from")
			t.Log("parsed time range with start only")
		})

		t.Run("should parse start and end", func(t *testing.T) {
			from, to, err := parseTimeRange("2026-02-02T19:00:00Z", "2026-02-02T20:00:00Z")
			require.NoError(t, err, "should not have error")
			expectedFrom, _ := time.Parse(time.RFC3339, "2026-02-02T19:00:00Z")
			expectedTo, _ := time.Parse(time.RFC3339, "2026-02-02T20:00:00Z")
			assert.Equal(t, expectedFrom, *from, "from time should match parsed start")
			assert.Equal(t, expectedTo, *to, "to time should match parsed end")
			t.Log("parsed time range with both start and end")
		})

		t.Run("should handle relative start times", func(t *testing.T) {
			from, to, err := parseTimeRange("now-2h", "")
			require.NoError(t, err, "should not have error parsing relative time")
			assert.NotNil(t, from, "from time should not be nil")
			assert.NotNil(t, to, "to time should not be nil")
			t.Log("parsed relative time range")
		})
	})
}

func Test_extractColValues(t *testing.T) {
	t.Run("test_response", func(t *testing.T) {
		t.Run("should extract values from valid response", func(t *testing.T) {
			resp := &grafana.DSQueryResponse{
				Results: map[string]grafana.DsQueryResult{
					"A": {
						Frames: []grafana.DsQueryFrame{
							{
								Schema: grafana.DsQueryFrameSchema{
									Fields: []grafana.DsQueryFrameField{
										{Name: "my_col"},
									},
								},
								Data: grafana.DSQueryFrameData{
									Values: [][]interface{}{
										{"val1", "val2"},
									},
								},
							},
						},
					},
				},
			}
			values, err := extractColValues(resp, "my_col")
			require.NoError(t, err, "should not have error")
			assert.NotNil(t, values, "values should not be nil")
			assert.Subset(t, *values, []string{"val1", "val2"}, "values should include extracted strings")
			t.Log("extracted valid col values")
		})

		t.Run("should propagate error from result", func(t *testing.T) {
			resp := &grafana.DSQueryResponse{
				Results: map[string]grafana.DsQueryResult{
					"A": {
						Error: "some target error",
					},
				},
			}
			values, err := extractColValues(resp, "my_col")
			assert.Error(t, err, "should have error")
			assert.Equal(t, "some target error", err.Error(), "error message should match")
			assert.Nil(t, values, "values should be nil")
			t.Log("handled error property in result")
		})
	})
}

func Test_parseQueryResponseFrames(t *testing.T) {
	t.Run("test_response", func(t *testing.T) {
		t.Run("should parse frames successfully", func(t *testing.T) {
			field1 := grafana.DsQueryFrameField{Name: "time"}
			field2 := grafana.DsQueryFrameField{Name: "_value", Labels: make(map[string]string)}
			field2.Labels["_field"] = "temp"

			resp := &grafana.DSQueryResponse{
				Results: map[string]grafana.DsQueryResult{
					"A": {
						Frames: []grafana.DsQueryFrame{
							{
								Schema: grafana.DsQueryFrameSchema{
									Name: "test_frame",
									Fields: []grafana.DsQueryFrameField{
										field1,
										field2,
									},
								},
								Data: grafana.DSQueryFrameData{
									Values: [][]interface{}{
										{1000, 2000},
										{22.5, 23.0},
									},
								},
							},
						},
					},
				},
			}
			frames, err := parseQueryResponseFrames(resp)
			require.NoError(t, err, "should not have error")
			require.Len(t, frames, 1, "should have 1 frame")
			assert.Equal(t, "test_frame", frames[0].Name, "frame name should match")
			assert.Subset(t, frames[0].Columns, []string{"time", "temp"}, "columns should be parsed and mapped")
			assert.Equal(t, uint(2), frames[0].RowCount, "should have 2 rows")
			t.Log("parsed frames successfully")
		})

		t.Run("should return error when results contain error", func(t *testing.T) {
			resp := &grafana.DSQueryResponse{
				Results: map[string]grafana.DsQueryResult{
					"A": {
						Error: "query failed",
					},
				},
			}
			frames, err := parseQueryResponseFrames(resp)
			assert.Error(t, err, "should return error")
			assert.Nil(t, frames, "frames should be nil")
			t.Log("returned error correctly for failed query")
		})

		t.Run("should return error when no rows", func(t *testing.T) {
			resp := &grafana.DSQueryResponse{
				Results: map[string]grafana.DsQueryResult{
					"A": {
						Frames: []grafana.DsQueryFrame{
							{
								Schema: grafana.DsQueryFrameSchema{
									Name: "test_frame",
									Fields: []grafana.DsQueryFrameField{
										{Name: "time"},
									},
								},
								Data: grafana.DSQueryFrameData{
									Values: [][]interface{}{},
								},
							},
						},
					},
				},
			}
			frames, err := parseQueryResponseFrames(resp)
			assert.Error(t, err, "should return error when no rows exist")
			assert.True(t, errors.Is(err, grafana.ErrNoRows), "error should be ErrNoRows")
			assert.Len(t, frames, 0, "frames should be empty")
			t.Log("returned no rows error correctly")
		})
	})
}
