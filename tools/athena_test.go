//go:build unit

package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubstituteAthenaMacros(t *testing.T) {
	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "timeFilter macro",
			query:    "SELECT * FROM logs WHERE $__timeFilter(request_time)",
			expected: "SELECT * FROM logs WHERE request_time BETWEEN TIMESTAMP '2024-01-15 10:00:00' AND TIMESTAMP '2024-01-15 11:00:00'",
		},
		{
			name:     "dateFilter macro",
			query:    "SELECT * FROM logs WHERE $__dateFilter(dt)",
			expected: "SELECT * FROM logs WHERE dt BETWEEN date '2024-01-15' AND date '2024-01-15'",
		},
		{
			name:     "unixEpochFilter macro",
			query:    "SELECT * FROM logs WHERE $__unixEpochFilter(epoch_col)",
			expected: "SELECT * FROM logs WHERE epoch_col BETWEEN 1705312800 AND 1705316400",
		},
		{
			name:     "timeFrom macro",
			query:    "SELECT * FROM logs WHERE ts > $__timeFrom()",
			expected: "SELECT * FROM logs WHERE ts > TIMESTAMP '2024-01-15 10:00:00'",
		},
		{
			name:     "timeTo macro",
			query:    "SELECT * FROM logs WHERE ts < $__timeTo()",
			expected: "SELECT * FROM logs WHERE ts < TIMESTAMP '2024-01-15 11:00:00'",
		},
		{
			name:     "$__from and $__to macros",
			query:    "SELECT * FROM logs WHERE ts BETWEEN $__from AND $__to",
			expected: "SELECT * FROM logs WHERE ts BETWEEN 1705312800000 AND 1705316400000",
		},
		{
			name:     "$__interval macro",
			query:    "SELECT date_trunc('second', ts / $__interval) AS bucket",
			expected: "SELECT date_trunc('second', ts / 3) AS bucket",
		},
		{
			name:     "$__interval_ms macro",
			query:    "SELECT ts / $__interval_ms AS bucket",
			expected: "SELECT ts / 3000 AS bucket",
		},
		{
			name:     "$__interval_ms not corrupted by $__interval",
			query:    "SELECT $__interval_ms, $__interval",
			expected: "SELECT 3000, 3",
		},
		{
			name:     "multiple macros",
			query:    "SELECT * FROM logs WHERE $__timeFilter(ts) AND val > $__from",
			expected: "SELECT * FROM logs WHERE ts BETWEEN TIMESTAMP '2024-01-15 10:00:00' AND TIMESTAMP '2024-01-15 11:00:00' AND val > 1705312800000",
		},
		{
			name:     "no macros unchanged",
			query:    "SELECT * FROM logs WHERE ts > '2024-01-01'",
			expected: "SELECT * FROM logs WHERE ts > '2024-01-01'",
		},
		{
			name:     "timeFilter with spaces around column",
			query:    "SELECT * FROM logs WHERE $__timeFilter( ts )",
			expected: "SELECT * FROM logs WHERE ts BETWEEN TIMESTAMP '2024-01-15 10:00:00' AND TIMESTAMP '2024-01-15 11:00:00'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := substituteAthenaMacros(tt.query, from, to)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnforceAthenaLimit(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		limit    int
		expected string
	}{
		{
			name:     "no limit - append default",
			query:    "SELECT * FROM logs",
			limit:    0,
			expected: "SELECT * FROM logs LIMIT 100",
		},
		{
			name:     "custom limit",
			query:    "SELECT * FROM logs",
			limit:    50,
			expected: "SELECT * FROM logs LIMIT 50",
		},
		{
			name:     "exceeds max - cap",
			query:    "SELECT * FROM logs",
			limit:    5000,
			expected: "SELECT * FROM logs LIMIT 1000",
		},
		{
			name:     "existing limit below max - unchanged",
			query:    "SELECT * FROM logs LIMIT 50",
			limit:    100,
			expected: "SELECT * FROM logs LIMIT 50",
		},
		{
			name:     "existing limit exceeds max - capped",
			query:    "SELECT * FROM logs LIMIT 5000",
			limit:    100,
			expected: "SELECT * FROM logs LIMIT 1000",
		},
		{
			name:     "trailing semicolon",
			query:    "SELECT * FROM logs;",
			limit:    100,
			expected: "SELECT * FROM logs LIMIT 100",
		},
		{
			name:     "case insensitive",
			query:    "SELECT * FROM logs limit 50",
			limit:    100,
			expected: "SELECT * FROM logs limit 50",
		},
		{
			name:     "negative limit uses default",
			query:    "SELECT * FROM logs",
			limit:    -1,
			expected: "SELECT * FROM logs LIMIT 100",
		},
		{
			name:     "SHOW COLUMNS unchanged",
			query:    "SHOW COLUMNS FROM mydb.mytable",
			limit:    0,
			expected: "SHOW COLUMNS FROM mydb.mytable",
		},
		{
			name:     "DESCRIBE unchanged",
			query:    "DESCRIBE mydb.mytable",
			limit:    0,
			expected: "DESCRIBE mydb.mytable",
		},
		{
			name:     "SHOW CREATE TABLE unchanged",
			query:    "SHOW CREATE TABLE mydb.mytable",
			limit:    0,
			expected: "SHOW CREATE TABLE mydb.mytable",
		},
		{
			name:     "show lowercase unchanged",
			query:    "show columns from mydb.mytable",
			limit:    0,
			expected: "show columns from mydb.mytable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := enforceAthenaLimit(tt.query, tt.limit)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateAthenaEmptyResultHints(t *testing.T) {
	hints := GenerateEmptyResultHints(HintContext{
		DatasourceType: "athena",
		Query:          "SELECT * FROM test",
	})

	assert.NotNil(t, hints)
	assert.Contains(t, hints.Summary, "Athena")
	found := false
	for _, action := range hints.SuggestedActions {
		if strings.Contains(action, "list_athena_tables") {
			found = true
			break
		}
	}
	assert.True(t, found, "Hints should suggest using list_athena_tables")
}

func TestAthenaQueryParams_Structure(t *testing.T) {
	params := AthenaQueryParams{
		DatasourceUID: "test-uid",
		Query:         "SELECT * FROM logs",
		Start:         "now-1h",
		End:           "now",
		Region:        "us-east-1",
		Catalog:       "AwsDataCatalog",
		Database:      "mydb",
		Variables:     map[string]string{"service": "my-app"},
		Limit:         100,
	}

	assert.Equal(t, "test-uid", params.DatasourceUID)
	assert.Equal(t, "SELECT * FROM logs", params.Query)
	assert.Equal(t, "us-east-1", params.Region)
	assert.Equal(t, "AwsDataCatalog", params.Catalog)
	assert.Equal(t, "mydb", params.Database)
	assert.Equal(t, "my-app", params.Variables["service"])
	assert.Equal(t, 100, params.Limit)
}

func TestAthenaQueryResult_Structure(t *testing.T) {
	result := AthenaQueryResult{
		Columns: []string{"request_time", "status", "method"},
		Rows: []map[string]interface{}{
			{"request_time": "2024-01-15T10:00:00Z", "status": float64(200), "method": "GET"},
		},
		RowCount:       1,
		ProcessedQuery: "SELECT request_time, status, method FROM logs LIMIT 100",
	}

	assert.Len(t, result.Columns, 3)
	assert.Len(t, result.Rows, 1)
	assert.Equal(t, 1, result.RowCount)
	assert.Equal(t, float64(200), result.Rows[0]["status"])
	assert.Contains(t, result.ProcessedQuery, "LIMIT 100")
}

func TestAthenaQueryResult_WithHints(t *testing.T) {
	result := AthenaQueryResult{
		Columns:  []string{"id"},
		Rows:     []map[string]interface{}{},
		RowCount: 0,
		Hints: &EmptyResultHints{
			Summary:          "No data found",
			PossibleCauses:   []string{"Table may not exist"},
			SuggestedActions: []string{"Check table name"},
		},
	}

	assert.NotNil(t, result.Hints)
	assert.Equal(t, "No data found", result.Hints.Summary)
	assert.Equal(t, 0, result.RowCount)
}

func TestSubstituteAthenaMacros_IntervalCalculation(t *testing.T) {
	tests := []struct {
		name             string
		rangeHours       int
		expectedInterval string
	}{
		{name: "1 hour range", rangeHours: 1, expectedInterval: "3"},
		{name: "6 hour range", rangeHours: 6, expectedInterval: "21"},
		{name: "24 hour range", rangeHours: 24, expectedInterval: "86"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
			to := from.Add(time.Duration(tt.rangeHours) * time.Hour)
			result := substituteAthenaMacros("$__interval", from, to)
			assert.Equal(t, tt.expectedInterval, result)
		})
	}
}

// marshalSDKResponse is a helper to properly marshal a backend.QueryDataResponse
// for use as an HTTP response body in test servers.
func marshalSDKResponse(t *testing.T, resp backend.QueryDataResponse) []byte {
	t.Helper()
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	return b
}

func TestAthenaResource_CorrectURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/datasources/uid/test-uid/resources/catalogs", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		var body map[string]string
		err := json.NewDecoder(r.Body).Decode(&body)
		assert.NoError(t, err)
		assert.Equal(t, "us-east-1", body["region"])

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]string{"AwsDataCatalog", "IcebergCatalog"})
	}))
	t.Cleanup(ts.Close)

	client := &athenaClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
		uid:        "test-uid",
	}

	respBytes, err := client.resource(t.Context(), "/catalogs", map[string]string{"region": "us-east-1"})
	require.NoError(t, err)

	var catalogs []string
	require.NoError(t, json.Unmarshal(respBytes, &catalogs))
	assert.Equal(t, []string{"AwsDataCatalog", "IcebergCatalog"}, catalogs)
}

func TestAthenaResource_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	client := &athenaClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
		uid:        "test-uid",
	}

	_, err := client.resource(t.Context(), "/catalogs", map[string]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestAthenaQuery_PayloadStructure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/ds/query", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		var payload map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&payload)
		assert.NoError(t, err)

		queries := payload["queries"].([]interface{})
		q := queries[0].(map[string]interface{})
		assert.Equal(t, "SELECT * FROM logs", q["rawSql"])
		assert.Equal(t, float64(AthenaFormatTable), q["format"])

		ds := q["datasource"].(map[string]interface{})
		assert.Equal(t, "test-uid", ds["uid"])
		assert.Equal(t, AthenaDatasourceType, ds["type"])

		connArgs := q["connectionArgs"].(map[string]interface{})
		assert.Equal(t, "us-east-1", connArgs["region"])
		assert.Equal(t, "AwsDataCatalog", connArgs["catalog"])
		assert.Equal(t, "mydb", connArgs["database"])

		assert.NotEmpty(t, payload["from"])
		assert.NotEmpty(t, payload["to"])

		w.Header().Set("Content-Type", "application/json")
		sdkResp := backend.QueryDataResponse{
			Responses: backend.Responses{
				"A": backend.DataResponse{
					Frames: data.Frames{
						data.NewFrame("",
							data.NewField("name", nil, []string{"Alice", "Bob"}),
							data.NewField("age", nil, []float64{30, 25}),
						),
					},
				},
			},
		}
		_, _ = w.Write(marshalSDKResponse(t, sdkResp))
	}))
	t.Cleanup(ts.Close)

	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	connArgs := map[string]interface{}{
		"region":   "us-east-1",
		"catalog":  "AwsDataCatalog",
		"database": "mydb",
	}

	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"datasource": map[string]string{
					"uid":  "test-uid",
					"type": AthenaDatasourceType,
				},
				"rawSql":         "SELECT * FROM logs",
				"refId":          "A",
				"format":         AthenaFormatTable,
				"connectionArgs": connArgs,
			},
		},
		"from": strconv.FormatInt(from.UnixMilli(), 10),
		"to":   strconv.FormatInt(to.UnixMilli(), 10),
	}

	resp, err := doDSQuery(t.Context(), http.DefaultClient, ts.URL, payload)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Responses, 1)
}

func TestAthenaQuery_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(ts.Close)

	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"datasource": map[string]string{
					"uid":  "test-uid",
					"type": AthenaDatasourceType,
				},
				"rawSql": "SELECT 1",
				"refId":  "A",
				"format": AthenaFormatTable,
			},
		},
		"from": strconv.FormatInt(from.UnixMilli(), 10),
		"to":   strconv.FormatInt(to.UnixMilli(), 10),
	}

	_, err := doDSQuery(t.Context(), http.DefaultClient, ts.URL, payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestAthenaQuery_FrameToRows(t *testing.T) {
	// Construct the response using SDK types to ensure proper marshal/unmarshal round-trip.
	sdkResp := backend.QueryDataResponse{
		Responses: backend.Responses{
			"A": backend.DataResponse{
				Frames: data.Frames{
					data.NewFrame("",
						data.NewField("name", nil, []string{"Alice", "Bob"}),
						data.NewField("age", nil, []float64{30, 25}),
					),
				},
			},
		},
	}

	// Marshal and unmarshal to simulate JSON round-trip (as in real doDSQuery).
	rawJSON, err := json.Marshal(sdkResp)
	require.NoError(t, err)

	var resp backend.QueryDataResponse
	require.NoError(t, json.Unmarshal(rawJSON, &resp))

	columns, rows, err := framesToTabularRows(&resp)
	require.NoError(t, err)

	assert.Equal(t, []string{"name", "age"}, columns)
	require.Len(t, rows, 2)
	assert.Equal(t, "Alice", rows[0]["name"])
	assert.Equal(t, "Bob", rows[1]["name"])
	assert.Equal(t, float64(30), rows[0]["age"])
	assert.Equal(t, float64(25), rows[1]["age"])
}

func TestAthenaQuery_EmptyFrame(t *testing.T) {
	sdkResp := backend.QueryDataResponse{
		Responses: backend.Responses{
			"A": backend.DataResponse{
				Frames: data.Frames{
					data.NewFrame("",
						data.NewField("id", nil, []float64{}),
					),
				},
			},
		},
	}

	rawJSON, err := json.Marshal(sdkResp)
	require.NoError(t, err)

	var resp backend.QueryDataResponse
	require.NoError(t, json.Unmarshal(rawJSON, &resp))

	_, rows, err := framesToTabularRows(&resp)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestAthenaQuery_ResultReuseInConnectionArgs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&payload)
		assert.NoError(t, err)

		queries := payload["queries"].([]interface{})
		q := queries[0].(map[string]interface{})
		connArgs := q["connectionArgs"].(map[string]interface{})
		assert.Equal(t, true, connArgs["resultReuseEnabled"])
		assert.Equal(t, float64(60), connArgs["resultReuseMaxAgeInMinutes"])

		w.Header().Set("Content-Type", "application/json")
		sdkResp := backend.QueryDataResponse{
			Responses: backend.Responses{
				"A": backend.DataResponse{
					Frames: data.Frames{},
				},
			},
		}
		_, _ = w.Write(marshalSDKResponse(t, sdkResp))
	}))
	t.Cleanup(ts.Close)

	from := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	to := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	connArgs := map[string]interface{}{
		"resultReuseEnabled":         true,
		"resultReuseMaxAgeInMinutes": 60,
	}

	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"datasource": map[string]string{
					"uid":  "test-uid",
					"type": AthenaDatasourceType,
				},
				"rawSql":         "SELECT 1",
				"refId":          "A",
				"format":         AthenaFormatTable,
				"connectionArgs": connArgs,
			},
		},
		"from": strconv.FormatInt(from.UnixMilli(), 10),
		"to":   strconv.FormatInt(to.UnixMilli(), 10),
	}

	resp, err := doDSQuery(t.Context(), http.DefaultClient, ts.URL, payload)
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestAthenaQuery_ErrorInResponse(t *testing.T) {
	// Construct with SDK types, marshal+unmarshal to simulate JSON round-trip.
	sdkResp := backend.QueryDataResponse{
		Responses: backend.Responses{
			"A": backend.DataResponse{
				Error: fmt.Errorf("SYNTAX_ERROR: line 1:1: Table 'nonexistent' does not exist"),
			},
		},
	}

	rawJSON, err := json.Marshal(sdkResp)
	require.NoError(t, err)

	var resp backend.QueryDataResponse
	require.NoError(t, json.Unmarshal(rawJSON, &resp))

	for _, r := range resp.Responses {
		assert.NotNil(t, r.Error)
		assert.Contains(t, r.Error.Error(), "does not exist")
	}
}
