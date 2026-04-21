//go:build unit

package tools

import (
	"errors"
	"strings"
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
}

func Test_enforceMeasurementsLimit(t *testing.T) {
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
}

func Test_enforceQueryLimit(t *testing.T) {
	t.Run("should wrap sql query and apply limit", func(t *testing.T) {
		args := InfluxQueryArgs{QueryType: SQLQueryType, Query: "SELECT * FROM my_table;", Limit: 10}
		enforceQueryLimit(&args)
		assert.Equal(t, "(SELECT * FROM my_table) LIMIT 10", args.Query, "sql query should be wrapped and limited")
		t.Log("applied sql limit")
	})

	t.Run("should apply flux limit", func(t *testing.T) {
		args := InfluxQueryArgs{QueryType: FluxQueryType, Query: "from(bucket: \"my-bucket\")", Limit: 20}
		enforceQueryLimit(&args)
		assert.Equal(t, "from(bucket: \"my-bucket\")\n|> limit(n:20)", args.Query, "flux query should be limited")
		t.Log("applied flux limit")
	})

	t.Run("should replace flux limit at absolute end", func(t *testing.T) {
		args := InfluxQueryArgs{QueryType: FluxQueryType, Query: "from(bucket: \"my-bucket\") |> limit(n:10)", Limit: 20}
		enforceQueryLimit(&args)
		assert.Equal(t, "from(bucket: \"my-bucket\") |> limit(n:20)", args.Query, "flux query should have limit replaced")
		t.Log("replaced absolute end flux limit")
	})

	t.Run("should append flux limit when existing limit is not at end", func(t *testing.T) {
		args := InfluxQueryArgs{QueryType: FluxQueryType, Query: "from(bucket: \"my-bucket\") |> limit(n:10) |> count()", Limit: 20}
		enforceQueryLimit(&args)
		assert.Equal(t, "from(bucket: \"my-bucket\") |> limit(n:10) |> count()\n|> limit(n:20)", args.Query, "flux query should have another limit appended at end")
		t.Log("appended flux limit after transformation")
	})

	t.Run("should handle whitespace and case-insensitivity in flux limit replacement", func(t *testing.T) {
		args := InfluxQueryArgs{QueryType: FluxQueryType, Query: "from(bucket: \"my-bucket\") |>  LIMIT ( n : 5 ) ", Limit: 20}
		enforceQueryLimit(&args)
		assert.Equal(t, "from(bucket: \"my-bucket\") |> limit(n:20)", args.Query, "flux query should normalize and replace limit")
		t.Log("normalized and replaced flux limit")
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

	t.Run("should replace excessive limit in CTE select part", func(t *testing.T) {
		args := InfluxQueryArgs{QueryType: SQLQueryType, Query: "WITH a AS (SELECT * FROM orders) SELECT * FROM a LIMIT 999999", Limit: 0}
		enforceQueryLimit(&args)
		assert.Equal(t, "WITH a AS (SELECT * FROM orders) (SELECT * FROM a LIMIT 999999) LIMIT 100", args.Query, "CTE query should have excessive limit replaced with enforced limit")
		t.Log("replaced excessive CTE limit")
	})

	t.Run("should replace excessive limit in multi-CTE select part", func(t *testing.T) {
		args := InfluxQueryArgs{QueryType: SQLQueryType, Query: "WITH a AS (SELECT * FROM orders), b AS (SELECT * FROM a) SELECT * FROM b LIMIT 5000", Limit: 0}
		enforceQueryLimit(&args)
		assert.Equal(t, "WITH a AS (SELECT * FROM orders), b AS (SELECT * FROM a) (SELECT * FROM b LIMIT 5000) LIMIT 100", args.Query, "multi-CTE query should have excessive limit replaced")
		t.Log("replaced excessive multi-CTE limit")
	})
}

func Test_parseTimeRange(t *testing.T) {
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
}

func Test_extractColValues(t *testing.T) {
	t.Run("should extract values from valid response", func(t *testing.T) {
		resp := &grafana.DSQueryResponse{
			Results: map[string]grafana.DSQueryResult{
				"A": {
					Frames: []grafana.DSQueryFrame{
						{
							Schema: grafana.DSQueryFrameSchema{
								Fields: []grafana.DSQueryFrameField{
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
		assert.Subset(t, values, []string{"val1", "val2"}, "values should include extracted strings")
		t.Log("extracted valid col values")
	})

	t.Run("should propagate error from result", func(t *testing.T) {
		resp := &grafana.DSQueryResponse{
			Results: map[string]grafana.DSQueryResult{
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
}

func Test_parseQueryResponseFrames(t *testing.T) {
	t.Run("test_response", func(t *testing.T) {
		t.Run("should parse frames successfully", func(t *testing.T) {
			field1 := grafana.DSQueryFrameField{Name: "time"}
			field2 := grafana.DSQueryFrameField{Name: "_value", Labels: make(map[string]string)}
			field2.Labels["_field"] = "temp"

			resp := &grafana.DSQueryResponse{
				Results: map[string]grafana.DSQueryResult{
					"A": {
						Frames: []grafana.DSQueryFrame{
							{
								Schema: grafana.DSQueryFrameSchema{
									Name: "test_frame",
									Fields: []grafana.DSQueryFrameField{
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
				Results: map[string]grafana.DSQueryResult{
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
				Results: map[string]grafana.DSQueryResult{
					"A": {
						Frames: []grafana.DSQueryFrame{
							{
								Schema: grafana.DSQueryFrameSchema{
									Name: "test_frame",
									Fields: []grafana.DSQueryFrameField{
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

func TestQuoting(t *testing.T) {
	t.Run("quoteStringAsFluxLiteral", func(t *testing.T) {
		assert.Equal(t, `"standard"`, quoteStringAsFluxLiteral("standard"))
		assert.Equal(t, `"with \"quotes\""`, quoteStringAsFluxLiteral(`with "quotes"`))
		assert.Equal(t, `"with \\backslashes\\"`, quoteStringAsFluxLiteral(`with \backslashes\`))
	})

	t.Run("quoteStringAsInfluxQLIdentifier", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
			message  string
		}{
			{
				input:    "standard",
				expected: `"standard"`,
				message:  "plain string with no special characters should just be wrapped in double quotes",
			},
			{
				input:    `with "quotes"`,
				expected: `"with \"quotes\""`,
				message:  "double quotes inside string should be escaped as \"",
			},
			{
				input:    `with \backslashes`,
				expected: `"with \\backslashes"`,
				message:  "backslashes should be escaped as \\\\ before quote escaping",
			},
			{
				input:    `trailing\`,
				expected: `"trailing\\"`,
				message:  "trailing backslash must be escaped to prevent unterminated identifier bug",
			},
			{
				input:    `slash\"quote`,
				expected: `"slash\\\"quote"`,
				message:  "backslash immediately before a double quote must both be escaped independently",
			},
			{
				input:    "",
				expected: `""`,
				message:  "empty string should produce a valid empty identifier",
			},
			{
				input:    `"`,
				expected: `"\""`,
				message:  "a lone double quote should be escaped as \"",
			},
			{
				input:    `\`,
				expected: `"\\"`,
				message:  "a lone backslash must be escaped to prevent unterminated identifier",
			},
		}

		for _, tt := range tests {
			t.Run(tt.message, func(t *testing.T) {
				assert.Equal(t, tt.expected, quoteStringAsInfluxQLIdentifier(tt.input), tt.message)
			})
		}
	})

	t.Run("quoteStringAsLiteral (SQL style)", func(t *testing.T) {
		assert.Equal(t, "'standard'", quoteStringAsLiteral("standard"))
		assert.Equal(t, "'it''s a test'", quoteStringAsLiteral("it's a test"))
	})
}

func TestFindTopLevelSelectAfterCTE(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantPos int    // -1 if not found, otherwise we just check query[pos:] starts with SELECT
		wantSel string // expected string at pos (trimmed, lowercased prefix)
	}{
		{
			name: "single CTE",
			query: `WITH a AS (
				SELECT * FROM orders
			)
			SELECT * FROM a`,
			wantSel: "SELECT * FROM a",
		},
		{
			name: "multiple CTEs",
			query: `WITH a AS (
				SELECT * FROM orders
			),
			b AS (
				SELECT COUNT(*) AS cnt FROM a
			)
			SELECT * FROM a JOIN b ON true`,
			wantSel: "SELECT * FROM a JOIN b ON true",
		},
		{
			name: "CTE with nested subquery inside",
			query: `WITH a AS (
				SELECT * FROM (SELECT id FROM orders WHERE id IN (SELECT id FROM archive)) sub
			)
			SELECT * FROM a`,
			wantSel: "SELECT * FROM a",
		},
		{
			name: `CTE with limit`,
			query: `
				WITH a AS (SELECT * FROM orders) SELECT * FROM a LIMIT 999999
			`,
			wantSel: `SELECT * FROM a LIMIT 999999`,
		},
		{
			name: "CTE with window function in body",
			query: `WITH ranked AS (
				SELECT *, ROW_NUMBER() OVER (PARTITION BY id ORDER BY created_at DESC) AS rn FROM orders
			)
			SELECT * FROM ranked WHERE rn = 1`,
			wantSel: "SELECT * FROM ranked WHERE rn = 1",
		},
		{
			name:    "not a CTE query",
			query:   `SELECT * FROM orders`,
			wantPos: -1,
		},
		{
			name:    "empty string",
			query:   ``,
			wantPos: -1,
		},
		{
			name:    "CTE with no final select (malformed)",
			query:   `WITH a AS (SELECT * FROM orders)`,
			wantPos: -1,
		},
		{
			name: "lowercase with",
			query: `with a as (
				select * from orders
			)
			select * from a`,
			wantSel: "select * from a",
		},
		{
			name: "three CTEs",
			query: `WITH a AS (SELECT * FROM t1),
			b AS (SELECT * FROM t2),
			c AS (SELECT * FROM a JOIN b ON a.id = b.id)
			SELECT * FROM c`,
			wantSel: "SELECT * FROM c",
		},
		{
			name: "CTE with deeply nested parens",
			query: `WITH a AS (
				SELECT * FROM (SELECT * FROM (SELECT * FROM orders) t1) t2
			)
			SELECT * FROM a`,
			wantSel: "SELECT * FROM a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos := findTopLevelSelectAfterCTE(tt.query)

			if tt.wantPos == -1 {
				if pos != -1 {
					t.Log(tt)
					t.Errorf("expected -1, got %d", pos)
				}
				return
			}

			if pos == -1 {
				t.Fatalf("expected a valid position, got -1")
			}

			got := strings.TrimSpace(tt.query[pos:])
			if !strings.EqualFold(got[:6], "select") {
				t.Errorf("expected SELECT at pos %d, got: %q", pos, got[:10])
			}

			if tt.wantSel != "" {
				gotTrimmed := strings.TrimSpace(got)
				wantTrimmed := strings.TrimSpace(tt.wantSel)
				if !strings.EqualFold(gotTrimmed, wantTrimmed) {
					t.Errorf("select part mismatch:\n  got:  %q\n  want: %q", gotTrimmed, wantTrimmed)
				}
			}
		})
	}
}
