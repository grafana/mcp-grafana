//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- parseGraphiteTime ---

func TestParseGraphiteTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name  string
		input string
		want  time.Time
		delta time.Duration
	}{
		{name: "empty uses default", input: "", want: now.Add(-time.Hour), delta: time.Minute},
		{name: "now", input: "now", want: now, delta: time.Minute},
		{name: "graphite hours", input: "-24h", want: now.Add(-24 * time.Hour), delta: time.Minute},
		{name: "graphite minutes", input: "-5min", want: now.Add(-5 * time.Minute), delta: time.Minute},
		{name: "graphite long unit", input: "-2weeks", want: now.Add(-14 * 24 * time.Hour), delta: time.Minute},
		{name: "graphite uppercase unit", input: "-1H", want: now.Add(-time.Hour), delta: time.Minute},
		{name: "grafana relative", input: "now-30m", want: now.Add(-30 * time.Minute), delta: time.Minute},
		{name: "RFC3339", input: "2024-01-01T00:00:00Z", want: time.Unix(1704067200, 0)},
		{name: "unix seconds", input: "1704067200", want: time.Unix(1704067200, 0)},
		{name: "YYYYMMDD", input: "20240101", want: time.Unix(1704067200, 0)},
		{name: "8-digit non-date is unix seconds", input: "12345678", want: time.Unix(12345678, 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGraphiteTime(tc.input, "-1h", parseStartTime)
			require.NoError(t, err)
			assert.WithinDuration(t, tc.want, got, tc.delta)
		})
	}

	// "-30m" and "-1h30m" would otherwise parse as negative Go durations,
	// i.e. times in the future.
	for _, input := range []string{"noon+yesterday", "-30m", "-1h30m", "-3600"} {
		_, err := parseGraphiteTime(input, "-1h", parseStartTime)
		assert.Error(t, err, input)
	}
}

// --- parseGraphiteDatapoints ---

func TestParseGraphiteDatapoints(t *testing.T) {
	t.Run("normal values", func(t *testing.T) {
		raw := [][]json.RawMessage{
			{json.RawMessage("1.5"), json.RawMessage("1704067200")},
			{json.RawMessage("2.0"), json.RawMessage("1704067260")},
		}
		pts := parseGraphiteDatapoints(raw)
		require.Len(t, pts, 2)
		require.NotNil(t, pts[0].Value)
		assert.InDelta(t, 1.5, *pts[0].Value, 1e-9)
		assert.Equal(t, int64(1704067200), pts[0].Timestamp)
		require.NotNil(t, pts[1].Value)
		assert.InDelta(t, 2.0, *pts[1].Value, 1e-9)
	})

	t.Run("null value becomes nil pointer", func(t *testing.T) {
		raw := [][]json.RawMessage{
			{json.RawMessage("null"), json.RawMessage("1704067200")},
		}
		pts := parseGraphiteDatapoints(raw)
		require.Len(t, pts, 1)
		assert.Nil(t, pts[0].Value)
		assert.Equal(t, int64(1704067200), pts[0].Timestamp)
	})

	t.Run("mix of null and non-null values", func(t *testing.T) {
		raw := [][]json.RawMessage{
			{json.RawMessage("null"), json.RawMessage("1704067200")},
			{json.RawMessage("3.14"), json.RawMessage("1704067260")},
			{json.RawMessage("null"), json.RawMessage("1704067320")},
		}
		pts := parseGraphiteDatapoints(raw)
		require.Len(t, pts, 3)
		assert.Nil(t, pts[0].Value)
		require.NotNil(t, pts[1].Value)
		assert.InDelta(t, 3.14, *pts[1].Value, 1e-9)
		assert.Nil(t, pts[2].Value)
	})

	t.Run("empty input returns empty slice", func(t *testing.T) {
		pts := parseGraphiteDatapoints(nil)
		assert.Empty(t, pts)
	})

	t.Run("malformed pairs are skipped", func(t *testing.T) {
		raw := [][]json.RawMessage{
			{json.RawMessage("1.0")}, // only one element — no timestamp
			{json.RawMessage("2.0"), json.RawMessage("1704067200")},
		}
		pts := parseGraphiteDatapoints(raw)
		require.Len(t, pts, 1)
		assert.Equal(t, int64(1704067200), pts[0].Timestamp)
	})
}

// --- queryGraphite via /api/ds/query ---

// graphiteDSQueryStub answers /api/ds/query with the given frames for refId A
// (or no result at all when frames is nil), and records the payload it received.
func graphiteDSQueryStub(frames data.Frames) (*httptest.Server, *map[string]interface{}) {
	payload := &map[string]interface{}{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/graphite":
			_, _ = io.WriteString(w, `{"uid":"graphite","type":"graphite"}`)
		case "/api/ds/query":
			_ = json.NewDecoder(r.Body).Decode(payload)
			resp := backend.QueryDataResponse{Responses: backend.Responses{}}
			if frames != nil {
				resp.Responses["A"] = backend.DataResponse{Frames: frames}
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})), payload
}

// graphiteFrame builds a frame shaped like the ones Grafana's Graphite
// datasource returns: one per series, values 60s apart from 1704067200.
func graphiteFrame(target string, tags data.Labels, values ...*float64) *data.Frame {
	times := make([]time.Time, len(values))
	for i := range values {
		times[i] = time.Unix(1704067200+int64(i)*60, 0)
	}
	return data.NewFrame("",
		data.NewField("time", nil, times),
		data.NewField("value", tags, values).SetConfig(&data.FieldConfig{DisplayNameFromDS: target}),
	)
}

func TestQueryGraphite_ConvertsFrames(t *testing.T) {
	v1, v2 := 0.5, 1.2
	server, payload := graphiteDSQueryStub(data.Frames{
		graphiteFrame("alias(servers.web01.cpu.load5)", data.Labels{"name": "servers.web01.cpu.load5"}, &v1, nil, &v2),
	})
	t.Cleanup(server.Close)

	result, err := queryGraphite(enforceTestCtx(server, false), QueryGraphiteParams{
		DatasourceUID: "graphite",
		Target:        "servers.web01.cpu.load5",
		From:          "2024-01-01T00:00:00Z",
		Until:         "2024-01-01T01:00:00Z",
	})
	require.NoError(t, err)
	assert.Nil(t, result.Hints)
	assert.Equal(t, []*GraphiteSeries{{
		Target: "alias(servers.web01.cpu.load5)",
		Tags:   map[string]string{"name": "servers.web01.cpu.load5"},
		Datapoints: []GraphiteDatapoint{
			{Value: &v1, Timestamp: 1704067200},
			{Value: nil, Timestamp: 1704067260},
			{Value: &v2, Timestamp: 1704067320},
		},
	}}, result.Series)

	assert.Equal(t, "1704067200000", (*payload)["from"])
	assert.Equal(t, "1704070800000", (*payload)["to"])
	queries := (*payload)["queries"].([]interface{})
	require.Len(t, queries, 1)
	q := queries[0].(map[string]interface{})
	assert.Equal(t, "servers.web01.cpu.load5", q["target"])
	assert.Equal(t, map[string]interface{}{"uid": "graphite", "type": "graphite"}, q["datasource"])
	// One point per second of the range, so Graphite never consolidates.
	assert.Equal(t, float64(3600), q["maxDataPoints"])
}

func TestQueryGraphite_PassesMaxDataPoints(t *testing.T) {
	server, payload := graphiteDSQueryStub(nil)
	t.Cleanup(server.Close)

	_, err := queryGraphite(enforceTestCtx(server, false), QueryGraphiteParams{
		DatasourceUID: "graphite", Target: "a.b", MaxDataPoints: 50,
	})
	require.NoError(t, err)
	q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, float64(50), q["maxDataPoints"])
}

func TestQueryGraphite_RejectsInvertedRange(t *testing.T) {
	_, err := queryGraphite(context.Background(), QueryGraphiteParams{
		DatasourceUID: "graphite", Target: "a.b", From: "now", Until: "-1h",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be before")
}

func TestGraphiteFramesToSeries_Error(t *testing.T) {
	_, err := graphiteFramesToSeries(&backend.QueryDataResponse{Responses: backend.Responses{
		"A": backend.ErrDataResponse(backend.StatusBadRequest, "request failed with error: bad target"),
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad target")
}

func TestQueryGraphite_EmptyResult_HasHints(t *testing.T) {
	server, _ := graphiteDSQueryStub(nil)
	t.Cleanup(server.Close)

	result, err := queryGraphite(enforceTestCtx(server, false), QueryGraphiteParams{
		DatasourceUID: "graphite", Target: "nonexistent.metric.*",
	})
	require.NoError(t, err)
	assert.Empty(t, result.Series)
	require.NotNil(t, result.Hints)
	assert.NotEmpty(t, result.Hints.Summary)
	assert.NotEmpty(t, result.Hints.PossibleCauses)
	assert.NotEmpty(t, result.Hints.SuggestedActions)
}

// --- listGraphiteMetrics handler ---

func TestListGraphiteMetrics_ParsesNodes(t *testing.T) {
	rawNodes := []graphiteRawMetricNode{
		{ID: "servers", Text: "servers", Leaf: 0, Expandable: 1},
		{ID: "cpu.load5", Text: "load5", Leaf: 1, Expandable: 0},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/metrics/find", r.URL.Path)
		assert.Equal(t, "servers.*", r.URL.Query().Get("query"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rawNodes)
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	ctx := context.Background()
	params := url.Values{}
	params.Set("query", "servers.*")

	data, err := client.doGet(ctx, "/metrics/find", params)
	require.NoError(t, err)

	var nodes []graphiteRawMetricNode
	require.NoError(t, json.Unmarshal(data, &nodes))
	require.Len(t, nodes, 2)

	parsed := make([]GraphiteMetricNode, 0, len(nodes))
	for _, n := range nodes {
		parsed = append(parsed, GraphiteMetricNode{
			ID:         n.ID,
			Text:       n.Text,
			Leaf:       n.Leaf == 1,
			Expandable: n.Expandable == 1,
		})
	}
	assert.False(t, parsed[0].Leaf)
	assert.True(t, parsed[0].Expandable)
	assert.True(t, parsed[1].Leaf)
	assert.False(t, parsed[1].Expandable)
}

// --- listGraphiteTags handler ---

func TestListGraphiteTags_ReturnsTags(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/tags", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag": "env", "count": 3},
			{"tag": "name", "count": 5},
			{"tag": "region", "count": 2},
			{"tag": "server", "count": 1},
		})
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	ctx := context.Background()
	data, err := client.doGet(ctx, "/tags", nil)
	require.NoError(t, err)

	var raw []struct {
		Tag string `json:"tag"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	result := make([]string, len(raw))
	for i, t := range raw {
		result[i] = t.Tag
	}
	assert.Equal(t, []string{"env", "name", "region", "server"}, result)
}

func TestListGraphiteTags_WithPrefix(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "env", r.URL.Query().Get("tagPrefix"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag": "env", "count": 3},
		})
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	ctx := context.Background()
	params := url.Values{}
	params.Set("tagPrefix", "env")

	data, err := client.doGet(ctx, "/tags", params)
	require.NoError(t, err)

	var raw []struct {
		Tag string `json:"tag"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	result := make([]string, len(raw))
	for i, t := range raw {
		result[i] = t.Tag
	}
	assert.Equal(t, []string{"env"}, result)
}

func TestListGraphiteTags_EmptyList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	data, err := client.doGet(context.Background(), "/tags", nil)
	require.NoError(t, err)

	var raw []struct {
		Tag string `json:"tag"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	result := make([]string, len(raw))
	for i, t := range raw {
		result[i] = t.Tag
	}
	assert.Empty(t, result)
}

// --- doGet error handling ---

func TestGraphiteClient_DoGet_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	_, err := client.doGet(context.Background(), "/render", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// --- computeSeriesDensity ---

func TestComputeSeriesDensity_AllNull(t *testing.T) {
	pts := []GraphiteDatapoint{
		{Value: nil, Timestamp: 1704067200},
		{Value: nil, Timestamp: 1704067260},
		{Value: nil, Timestamp: 1704067320},
	}
	s := computeSeriesDensity("my.metric", pts)
	assert.Equal(t, "my.metric", s.Target)
	assert.InDelta(t, 0.0, s.FillRatio, 1e-9)
	assert.Equal(t, 3, s.TotalPoints)
	assert.Equal(t, 0, s.NonNullPoints)
	assert.Nil(t, s.LastSeen)
	assert.Equal(t, int64(180), s.LongestGapSec) // 3 nulls × 60 s/step
	assert.Equal(t, int64(60), s.EstimatedInterval)
}

func TestComputeSeriesDensity_AllNonNull(t *testing.T) {
	v1, v2, v3 := 1.0, 2.0, 3.0
	pts := []GraphiteDatapoint{
		{Value: &v1, Timestamp: 1704067200},
		{Value: &v2, Timestamp: 1704067260},
		{Value: &v3, Timestamp: 1704067320},
	}
	s := computeSeriesDensity("my.metric", pts)
	assert.InDelta(t, 1.0, s.FillRatio, 1e-9)
	assert.Equal(t, 3, s.TotalPoints)
	assert.Equal(t, 3, s.NonNullPoints)
	require.NotNil(t, s.LastSeen)
	assert.Equal(t, int64(1704067320), *s.LastSeen)
	assert.Equal(t, int64(0), s.LongestGapSec)
	assert.Equal(t, int64(60), s.EstimatedInterval)
}

func TestComputeSeriesDensity_Mixed(t *testing.T) {
	v := 5.5
	pts := []GraphiteDatapoint{
		{Value: nil, Timestamp: 1704067200},
		{Value: &v, Timestamp: 1704067260},
		{Value: nil, Timestamp: 1704067320},
		{Value: nil, Timestamp: 1704067380},
	}
	s := computeSeriesDensity("my.metric", pts)
	assert.InDelta(t, 0.25, s.FillRatio, 1e-9)
	assert.Equal(t, 4, s.TotalPoints)
	assert.Equal(t, 1, s.NonNullPoints)
	require.NotNil(t, s.LastSeen)
	assert.Equal(t, int64(1704067260), *s.LastSeen)
	assert.Equal(t, int64(120), s.LongestGapSec) // trailing 2-null run × 60 s
	assert.Equal(t, int64(60), s.EstimatedInterval)
}

func TestComputeSeriesDensity_Empty(t *testing.T) {
	s := computeSeriesDensity("my.metric", nil)
	assert.Equal(t, "my.metric", s.Target)
	assert.InDelta(t, 0.0, s.FillRatio, 1e-9)
	assert.Equal(t, 0, s.TotalPoints)
	assert.Equal(t, 0, s.NonNullPoints)
	assert.Nil(t, s.LastSeen)
	assert.Equal(t, int64(0), s.LongestGapSec)
	assert.Equal(t, int64(0), s.EstimatedInterval)
}

func TestComputeSeriesDensity_SingleNonNull(t *testing.T) {
	v := 1.0
	pts := []GraphiteDatapoint{{Value: &v, Timestamp: 1704067200}}
	s := computeSeriesDensity("my.metric", pts)
	assert.InDelta(t, 1.0, s.FillRatio, 1e-9)
	assert.Equal(t, 1, s.TotalPoints)
	assert.Equal(t, 1, s.NonNullPoints)
	require.NotNil(t, s.LastSeen)
	assert.Equal(t, int64(1704067200), *s.LastSeen)
	assert.Equal(t, int64(0), s.LongestGapSec)
	assert.Equal(t, int64(0), s.EstimatedInterval) // can't infer from 1 point
}

func TestComputeSeriesDensity_LongestGapInMiddle(t *testing.T) {
	v := 1.0
	pts := []GraphiteDatapoint{
		{Value: &v, Timestamp: 1704067200},
		{Value: nil, Timestamp: 1704067260},
		{Value: nil, Timestamp: 1704067320},
		{Value: nil, Timestamp: 1704067380},
		{Value: &v, Timestamp: 1704067440},
		{Value: nil, Timestamp: 1704067500},
	}
	s := computeSeriesDensity("my.metric", pts)
	// Middle gap: 3 nulls = 180 s; trailing gap: 1 null = 60 s
	assert.Equal(t, int64(180), s.LongestGapSec)
	assert.Equal(t, 2, s.NonNullPoints)
	require.NotNil(t, s.LastSeen)
	assert.Equal(t, int64(1704067440), *s.LastSeen)
}

// --- query_graphite_density full-flow via /api/ds/query ---

func TestQueryGraphiteDensity_AllNullCluster(t *testing.T) {
	// Primary use-case: wildcard target where every node is all-null.
	server, _ := graphiteDSQueryStub(data.Frames{
		graphiteFrame("obox-cl1.sys.sessions", nil, nil, nil, nil),
		graphiteFrame("obox-cl2.sys.sessions", nil, nil, nil, nil),
	})
	t.Cleanup(server.Close)

	result, err := queryGraphiteDensity(enforceTestCtx(server, false), QueryGraphiteDensityParams{
		DatasourceUID: "graphite", Target: "obox-cl*.sys.sessions",
	})
	require.NoError(t, err)
	require.Len(t, result.Series, 2)
	for _, s := range result.Series {
		assert.InDelta(t, 0.0, s.FillRatio, 1e-9, "series %s", s.Target)
		assert.Equal(t, 0, s.NonNullPoints, "series %s", s.Target)
		assert.Nil(t, s.LastSeen, "series %s", s.Target)
	}
}

func TestQueryGraphiteDensity_MixedCluster(t *testing.T) {
	v5, v6 := 5.0, 6.0
	server, _ := graphiteDSQueryStub(data.Frames{
		graphiteFrame("obox-cl1.sys.sessions", nil, nil, nil, nil),
		graphiteFrame("obox-cl2.sys.sessions", nil, &v5, nil, &v6),
	})
	t.Cleanup(server.Close)

	result, err := queryGraphiteDensity(enforceTestCtx(server, false), QueryGraphiteDensityParams{
		DatasourceUID: "graphite", Target: "obox-cl*.sys.sessions",
	})
	require.NoError(t, err)
	require.Len(t, result.Series, 2)

	// cl1: all null
	s1 := result.Series[0]
	assert.Equal(t, "obox-cl1.sys.sessions", s1.Target)
	assert.InDelta(t, 0.0, s1.FillRatio, 1e-9)
	assert.Equal(t, 3, s1.TotalPoints)
	assert.Equal(t, 0, s1.NonNullPoints)
	assert.Nil(t, s1.LastSeen)
	assert.Equal(t, int64(180), s1.LongestGapSec)

	// cl2: mixed
	s2 := result.Series[1]
	assert.Equal(t, "obox-cl2.sys.sessions", s2.Target)
	assert.InDelta(t, 2.0/3.0, s2.FillRatio, 1e-9)
	assert.Equal(t, 3, s2.TotalPoints)
	assert.Equal(t, 2, s2.NonNullPoints)
	require.NotNil(t, s2.LastSeen)
	assert.Equal(t, int64(1704067320), *s2.LastSeen)
	assert.Equal(t, int64(60), s2.LongestGapSec)
	assert.Equal(t, int64(60), s2.EstimatedInterval)
}
