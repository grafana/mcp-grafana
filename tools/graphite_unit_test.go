package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- parseGraphiteTime ---

func TestParseGraphiteTime(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string passes through",
			input: "",
			want:  "",
		},
		{
			name:  "now passes through",
			input: "now",
			want:  "now",
		},
		{
			name:  "relative -1h passes through",
			input: "-1h",
			want:  "-1h",
		},
		{
			name:  "relative -24h passes through",
			input: "-24h",
			want:  "-24h",
		},
		{
			name:  "RFC3339 is converted to unix timestamp",
			input: "2024-01-01T00:00:00Z",
			want:  "1704067200",
		},
		{
			name:  "unknown format passes through",
			input: "12:00_20240101",
			want:  "12:00_20240101",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGraphiteTime(tc.input)
			assert.Equal(t, tc.want, got)
		})
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

// --- queryGraphite handler (via doGet) ---

func TestQueryGraphite_DoGet_ParsesRenderResponse(t *testing.T) {
	renderResp := []graphiteRawSeries{
		{
			Target: "servers.web01.cpu.load5",
			Datapoints: [][]json.RawMessage{
				{json.RawMessage("0.5"), json.RawMessage("1704067200")},
				{json.RawMessage("null"), json.RawMessage("1704067260")},
				{json.RawMessage("1.2"), json.RawMessage("1704067320")},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/render", r.URL.Path)
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "servers.web01.cpu.load5", r.URL.Query().Get("target"))
		assert.Equal(t, "json", r.URL.Query().Get("format"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(renderResp)
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	params := url.Values{}
	params.Set("target", "servers.web01.cpu.load5")
	params.Set("from", "-1h")
	params.Set("until", "now")
	params.Set("format", "json")

	data, err := client.doGet(context.Background(), "/render", params)
	require.NoError(t, err)

	var series []graphiteRawSeries
	require.NoError(t, json.Unmarshal(data, &series))
	require.Len(t, series, 1)
	assert.Equal(t, "servers.web01.cpu.load5", series[0].Target)

	pts := parseGraphiteDatapoints(series[0].Datapoints)
	require.Len(t, pts, 3)
	require.NotNil(t, pts[0].Value)
	assert.InDelta(t, 0.5, *pts[0].Value, 1e-9)
	assert.Nil(t, pts[1].Value)
	require.NotNil(t, pts[2].Value)
	assert.InDelta(t, 1.2, *pts[2].Value, 1e-9)
}

func TestQueryGraphite_EmptyResult_HasHints(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	ctx := context.Background()
	data, err := client.doGet(ctx, "/render", nil)
	require.NoError(t, err)

	var rawSeries []graphiteRawSeries
	require.NoError(t, json.Unmarshal(data, &rawSeries))
	assert.Empty(t, rawSeries)

	// Simulate the handler building hints for an empty result
	hints := GenerateEmptyResultHints(HintContext{
		DatasourceType: GraphiteDatasourceType,
		Query:          "nonexistent.metric.*",
		StartTime:      time.Now().Add(-time.Hour),
		EndTime:        time.Now(),
	})
	require.NotNil(t, hints)
	assert.NotEmpty(t, hints.Summary)
	assert.NotEmpty(t, hints.PossibleCauses)
	assert.NotEmpty(t, hints.SuggestedActions)
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

// --- query_graphite_density full-flow via client ---

func TestQueryGraphiteDensity_AllNullCluster(t *testing.T) {
	// Primary use-case: wildcard target where every node is all-null.
	// Verifies that the raw-series path returns fillRatio=0 and lastSeen=nil
	// for every series.
	rawResp := []graphiteRawSeries{
		{
			Target: "obox-cl1.sys.sessions",
			Datapoints: [][]json.RawMessage{
				{json.RawMessage("null"), json.RawMessage("1704067200")},
				{json.RawMessage("null"), json.RawMessage("1704067260")},
				{json.RawMessage("null"), json.RawMessage("1704067320")},
			},
		},
		{
			Target: "obox-cl2.sys.sessions",
			Datapoints: [][]json.RawMessage{
				{json.RawMessage("null"), json.RawMessage("1704067200")},
				{json.RawMessage("null"), json.RawMessage("1704067260")},
				{json.RawMessage("null"), json.RawMessage("1704067320")},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rawResp)
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{httpClient: http.DefaultClient, baseURL: ts.URL}

	params := url.Values{}
	params.Set("target", "obox-cl*.sys.sessions")
	params.Set("from", "-1h")
	params.Set("until", "now")
	params.Set("format", "json")

	data, err := client.doGet(context.Background(), "/render", params)
	require.NoError(t, err)

	var raw []graphiteRawSeries
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Len(t, raw, 2)

	for _, rs := range raw {
		pts := parseGraphiteDatapoints(rs.Datapoints)
		s := computeSeriesDensity(rs.Target, pts)
		assert.InDelta(t, 0.0, s.FillRatio, 1e-9, "series %s", rs.Target)
		assert.Equal(t, 0, s.NonNullPoints, "series %s", rs.Target)
		assert.Nil(t, s.LastSeen, "series %s", rs.Target)
	}
}

func TestQueryGraphiteDensity_MixedCluster(t *testing.T) {
	v5, v6 := 5.0, 6.0
	rawResp := []graphiteRawSeries{
		{
			Target: "obox-cl1.sys.sessions",
			Datapoints: [][]json.RawMessage{
				{json.RawMessage("null"), json.RawMessage("1704067200")},
				{json.RawMessage("null"), json.RawMessage("1704067260")},
				{json.RawMessage("null"), json.RawMessage("1704067320")},
			},
		},
		{
			Target: "obox-cl2.sys.sessions",
			Datapoints: [][]json.RawMessage{
				{json.RawMessage("5.0"), json.RawMessage("1704067200")},
				{json.RawMessage("null"), json.RawMessage("1704067260")},
				{json.RawMessage("6.0"), json.RawMessage("1704067320")},
			},
		},
	}
	_ = v5
	_ = v6

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rawResp)
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{httpClient: http.DefaultClient, baseURL: ts.URL}

	params := url.Values{}
	params.Set("target", "obox-cl*.sys.sessions")
	params.Set("format", "json")

	data, err := client.doGet(context.Background(), "/render", params)
	require.NoError(t, err)

	var raw []graphiteRawSeries
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Len(t, raw, 2)

	// cl1: all null
	s1 := computeSeriesDensity(raw[0].Target, parseGraphiteDatapoints(raw[0].Datapoints))
	assert.Equal(t, "obox-cl1.sys.sessions", s1.Target)
	assert.InDelta(t, 0.0, s1.FillRatio, 1e-9)
	assert.Equal(t, 3, s1.TotalPoints)
	assert.Equal(t, 0, s1.NonNullPoints)
	assert.Nil(t, s1.LastSeen)
	assert.Equal(t, int64(180), s1.LongestGapSec)

	// cl2: mixed
	s2 := computeSeriesDensity(raw[1].Target, parseGraphiteDatapoints(raw[1].Datapoints))
	assert.Equal(t, "obox-cl2.sys.sessions", s2.Target)
	assert.InDelta(t, 2.0/3.0, s2.FillRatio, 1e-9)
	assert.Equal(t, 3, s2.TotalPoints)
	assert.Equal(t, 2, s2.NonNullPoints)
	require.NotNil(t, s2.LastSeen)
	assert.Equal(t, int64(1704067320), *s2.LastSeen)
	assert.Equal(t, int64(60), s2.LongestGapSec)
	assert.Equal(t, int64(60), s2.EstimatedInterval)
}
