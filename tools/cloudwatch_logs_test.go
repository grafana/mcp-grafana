//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCloudWatchNamedItems(t *testing.T) {
	t.Run("log groups", func(t *testing.T) {
		result, err := parseCloudWatchNamedItems([]byte(`[{"value":{"arn":"arn:aws:logs:us-east-1:123:log-group:/ecs/prod","name":"/ecs/prod"},"accountId":"123"},{"value":{"arn":"x","name":"/ecs/staging"}}]`))
		require.NoError(t, err)
		assert.Equal(t, []string{"/ecs/prod", "/ecs/staging"}, result)
	})

	t.Run("log group fields", func(t *testing.T) {
		result, err := parseCloudWatchNamedItems([]byte(`[{"value":{"name":"@timestamp","percent":100}},{"value":{"name":"level","percent":50}}]`))
		require.NoError(t, err)
		assert.Equal(t, []string{"@timestamp", "level"}, result)
	})

	t.Run("empty", func(t *testing.T) {
		result, err := parseCloudWatchNamedItems([]byte(`[]`))
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := parseCloudWatchNamedItems([]byte(`not json`))
		require.Error(t, err)
	})
}

func TestParseLogsFrames(t *testing.T) {
	ts := time.UnixMilli(1705312800000)
	frame := data.NewFrame("A",
		data.NewField("@timestamp", nil, []*time.Time{&ts, &ts, nil}),
		data.NewField("@message", nil, []string{"ERROR: boom", "INFO: ok", "WARN: hmm"}),
		data.NewField("count", nil, []float64{1.5, 2, 3}),
		data.NewField("@ptr", nil, []string{"p1", "p2", "p3"}),
		data.NewField("__log__grafana_internal__", nil, []string{"a", "b", "c"}),
	)
	frame.Meta = &data.FrameMeta{
		Custom: map[string]interface{}{"Status": "Complete"},
		Stats:  []data.QueryStat{{FieldConfig: data.FieldConfig{DisplayName: "Records matched"}, Value: 5000}},
	}

	result := parseLogsFrames(data.Frames{frame}, 2)

	assert.Equal(t, "Complete", result.Status)
	assert.Equal(t, 5000, result.TotalFound, "totalFound reports records matched, not rows returned")
	require.Len(t, result.Logs, 2)
	assert.Equal(t, map[string]string{
		"@timestamp": "2024-01-15T10:00:00Z",
		"@message":   "ERROR: boom",
		"count":      "1.5",
	}, result.Logs[0])
	assert.Empty(t, result.Hints)
}

func TestParseLogsFrames_Empty(t *testing.T) {
	frame := data.NewFrame("A", data.NewField("@message", nil, []string{}))
	result := parseLogsFrames(data.Frames{frame}, 100)

	assert.Equal(t, 0, result.TotalFound)
	assert.NotNil(t, result.Logs)
	hints := strings.Join(result.Hints, " ")
	assert.Contains(t, hints, "list_cloudwatch_log_groups")
	assert.Contains(t, hints, "list_cloudwatch_log_group_fields")
}

func TestLogsQueryStatus(t *testing.T) {
	withMeta := func(custom interface{}) data.Frames {
		f := data.NewFrame("A")
		f.Meta = &data.FrameMeta{Custom: custom}
		return data.Frames{f}
	}

	assert.Equal(t, "Running", logsQueryStatus(withMeta(map[string]interface{}{"Status": "Running"})))
	assert.Equal(t, "", logsQueryStatus(withMeta("timeSeriesQuery")), "metrics frames carry a string custom meta")
	assert.Equal(t, "", logsQueryStatus(data.Frames{data.NewFrame("A")}))
	assert.Equal(t, "", logsQueryStatus(nil))
}

// fakeLogsServer serves /api/ds/query for StartQuery and GetQueryResults,
// returning the given GetQueryResults statuses in order (the last repeats).
func fakeLogsServer(t *testing.T, statuses ...string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var queries []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Queries []map[string]any `json:"queries"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Len(t, payload.Queries, 1)
		q := payload.Queries[0]
		queries = append(queries, q)

		w.Header().Set("Content-Type", "application/json")
		if q["subtype"] == "StartQuery" {
			_, _ = w.Write([]byte(`{"results":{"A":{"frames":[{"schema":{"fields":[{"name":"queryId","type":"string","typeInfo":{"frame":"string"}}]},"data":{"values":[["qid-1"]]}}]}}}`))
			return
		}
		i := min(len(queries)-2, len(statuses)-1)
		_, _ = w.Write([]byte(`{"results":{"A":{"frames":[{"schema":{"meta":{"custom":{"Status":"` + statuses[i] + `"}},"fields":[{"name":"@message","type":"string","typeInfo":{"frame":"string"}}]},"data":{"values":[["hello"]]}}]}}}`))
	}))
	t.Cleanup(server.Close)
	return server, &queries
}

func TestRunLogsQuery(t *testing.T) {
	args := QueryCloudWatchLogsParams{
		DatasourceUID: "cw",
		Region:        "us-east-1",
		LogGroupNames: []string{"app"},
		QueryString:   "fields @message",
		Limit:         100,
	}
	now := time.Now()

	t.Run("polls until complete", func(t *testing.T) {
		server, queries := fakeLogsServer(t, "Running", "Complete")
		client := &cloudWatchClient{httpClient: server.Client(), baseURL: server.URL}

		frames, err := client.runLogsQuery(context.Background(), args, now.Add(-time.Hour), now, 5*time.Second)
		require.NoError(t, err)

		result := parseLogsFrames(frames, 100)
		assert.Equal(t, []map[string]string{{"@message": "hello"}}, result.Logs)

		require.Len(t, *queries, 3, "a completed query is not stopped")
		start, poll := (*queries)[0], (*queries)[1]
		_, pollHasLimit := poll["limit"]
		assert.False(t, pollHasLimit)
		assert.Equal(t, "fields @message", start["queryString"])
		assert.Equal(t, []any{"app"}, start["logGroupNames"])
		assert.Equal(t, float64(100), start["limit"])
		assert.Equal(t, "GetQueryResults", poll["subtype"])
		assert.Equal(t, "qid-1", poll["queryId"])
	})

	t.Run("failed status", func(t *testing.T) {
		server, _ := fakeLogsServer(t, "Failed")
		client := &cloudWatchClient{httpClient: server.Client(), baseURL: server.URL}

		_, err := client.runLogsQuery(context.Background(), args, now.Add(-time.Hour), now, 5*time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status Failed")
	})

	t.Run("times out", func(t *testing.T) {
		server, queries := fakeLogsServer(t, "Running")
		client := &cloudWatchClient{httpClient: server.Client(), baseURL: server.URL}

		_, err := client.runLogsQuery(context.Background(), args, now.Add(-time.Hour), now, 500*time.Millisecond)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")

		last := (*queries)[len(*queries)-1]
		assert.Equal(t, "StopQuery", last["subtype"], "a timed-out query is stopped")
		assert.Equal(t, "qid-1", last["queryId"])
	})

	t.Run("poll error stops the query", func(t *testing.T) {
		var subtypes []any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload struct {
				Queries []map[string]any `json:"queries"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			subtypes = append(subtypes, payload.Queries[0]["subtype"])
			if payload.Queries[0]["subtype"] == "GetQueryResults" {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"results":{"A":{"frames":[{"schema":{"fields":[{"name":"queryId","type":"string","typeInfo":{"frame":"string"}}]},"data":{"values":[["qid-1"]]}}]}}}`))
		}))
		defer server.Close()
		client := &cloudWatchClient{httpClient: server.Client(), baseURL: server.URL}

		_, err := client.runLogsQuery(context.Background(), args, now.Add(-time.Hour), now, 5*time.Second)
		require.Error(t, err)
		assert.Equal(t, []any{"StartQuery", "GetQueryResults", "StopQuery"}, subtypes)
	})

	t.Run("query error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"results":{"A":{"error":"MalformedQueryException: bad query"}}}`))
		}))
		defer server.Close()
		client := &cloudWatchClient{httpClient: server.Client(), baseURL: server.URL}

		_, err := client.runLogsQuery(context.Background(), args, now.Add(-time.Hour), now, 5*time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "MalformedQueryException")
	})
}
