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
	tags := []string{"env", "name", "region", "server"}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/tags", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tags)
	}))
	t.Cleanup(ts.Close)

	client := &GraphiteClient{
		httpClient: http.DefaultClient,
		baseURL:    ts.URL,
	}

	ctx := context.Background()
	data, err := client.doGet(ctx, "/tags", nil)
	require.NoError(t, err)

	var result []string
	require.NoError(t, json.Unmarshal(data, &result))
	assert.Equal(t, tags, result)
}

func TestListGraphiteTags_WithPrefix(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "env", r.URL.Query().Get("tagPrefix"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]string{"env"})
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

	var result []string
	require.NoError(t, json.Unmarshal(data, &result))
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

	var result []string
	require.NoError(t, json.Unmarshal(data, &result))
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
