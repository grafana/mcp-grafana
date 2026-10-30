package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/client"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryPyroscope_QueryTypeValidation(t *testing.T) {
	tests := []struct {
		name      string
		queryType string
		wantErr   string
	}{
		{name: "invalid rejected", queryType: "unknown", wantErr: `invalid query_type "unknown"`},
		{name: "typo rejected", queryType: "profle", wantErr: `invalid query_type "profle"`},
		{name: "number rejected", queryType: "123", wantErr: `invalid query_type "123"`},
		{name: "plural profiles rejected", queryType: "profiles", wantErr: `invalid query_type "profiles"`},
		{name: "singular metric rejected", queryType: "metric", wantErr: `invalid query_type "metric"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := queryPyroscope(t.Context(), QueryPyroscopeParams{
				DataSourceUID: "fake",
				ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
				QueryType:     tc.queryType,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestQueryPyroscope_FormatValidation(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr string
	}{
		{name: "invalid rejected", format: "unknown", wantErr: `invalid format "unknown"`},
		{name: "flamegraph rejected", format: "flamegraph", wantErr: `invalid format "flamegraph"`},
		{name: "json rejected", format: "json", wantErr: `invalid format "json"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := queryPyroscope(t.Context(), QueryPyroscopeParams{
				DataSourceUID: "fake",
				ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
				Format:        tc.format,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestBuildProfileTable(t *testing.T) {
	table, err := buildProfileTable(testFlameNodes(t), "nanoseconds", 100)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(table, "\n"), "\n")
	require.Len(t, lines, 7, table)

	assert.Equal(t, "Total: 180ns", lines[0])
	assert.Equal(t, "Showing top 3 out of 3 functions by flat (self) value, accounting for 83.33% of the total", lines[1])
	assert.Contains(t, lines[2], `16.67% of the total was collapsed into an "other" bucket`)
	assert.Contains(t, lines[3], "flat%")
	assert.Contains(t, lines[3], "cum%")

	// Ranked by flat; recursion counted once in cum; entry gets cum only.
	assert.Regexp(t, `^\s+100ns\s+55.56%\s+55.56%\s+100ns\s+55.56%\s+main.leaf$`, lines[4])
	assert.Regexp(t, `^\s+50ns\s+27.78%\s+83.33%\s+150ns\s+83.33%\s+main.mid$`, lines[5])
	assert.Regexp(t, `^\s+0\S*\s+0.00%\s+83.33%\s+150ns\s+83.33%\s+main.entry$`, lines[6])

	assert.NotContains(t, table, `  other`, "truncation bucket must not appear as a function row")
	assert.NotContains(t, table, `  total`, "synthetic root must not appear as a function row")
}

func TestBuildProfileTable_MaxRows(t *testing.T) {
	table, err := buildProfileTable(testFlameNodes(t), "nanoseconds", 1)
	require.NoError(t, err)

	assert.Contains(t, table, "Showing top 1 out of 3 functions by flat (self) value, accounting for 55.56% of the total")
	assert.Contains(t, table, "main.leaf")
	assert.NotContains(t, table, "main.mid")
}

func TestBuildProfileTable_Empty(t *testing.T) {
	_, err := buildProfileTable([]flameNode{{name: "total", parent: -1}}, "nanoseconds", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty profile")

	_, err = buildProfileTable(nil, "nanoseconds", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty profile")
}

func TestSampleUnitFromProfileType(t *testing.T) {
	assert.Equal(t, "nanoseconds", sampleUnitFromProfileType("process_cpu:cpu:nanoseconds:cpu:nanoseconds"))
	assert.Equal(t, "bytes", sampleUnitFromProfileType("memory:alloc_space:bytes:space:bytes"))
	assert.Equal(t, "", sampleUnitFromProfileType("garbage"))
}

func TestFormatSampleValue(t *testing.T) {
	assert.Equal(t, "615.26s", formatSampleValue(615_260_000_000, "nanoseconds"))
	assert.Equal(t, "1.50ms", formatSampleValue(1_500_000, "nanoseconds"))
	assert.Equal(t, "2.00us", formatSampleValue(2_000, "nanoseconds"))
	assert.Equal(t, "999ns", formatSampleValue(999, "nanoseconds"))
	assert.Equal(t, "1.42GB", formatSampleValue(1_525_000_000, "bytes"))
	assert.Equal(t, "1.45MB", formatSampleValue(1_525_000, "bytes"))
	assert.Equal(t, "1.49kB", formatSampleValue(1_525, "bytes"))
	assert.Equal(t, "42B", formatSampleValue(42, "bytes"))
	assert.Equal(t, "12345", formatSampleValue(12345, "count"))
}

// TestListPyroscopeLabels_EmptyReturnsEmptySlice guards against the label
// tools serialising a nil slice as JSON null when Pyroscope has no data.
func TestListPyroscopeLabels_EmptyReturnsEmptySlice(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasources/uid/test-pyro", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uid": "test-pyro", "type": "grafana-pyroscope-datasource", "id": 1})
	})
	// An empty proto message encodes to zero bytes, i.e. no names.
	mux.HandleFunc("/api/datasources/proxy/uid/test-pyro/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/proto")
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	u, _ := url.Parse(ts.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	ctx := mcpgrafana.WithGrafanaClient(
		mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: ts.URL}),
		&mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg)},
	)

	names, err := listPyroscopeLabelNames(ctx, ListPyroscopeLabelNamesParams{DataSourceUID: "test-pyro"})
	require.NoError(t, err)
	assert.Equal(t, []string{}, names)

	values, err := listPyroscopeLabelValues(ctx, ListPyroscopeLabelValuesParams{DataSourceUID: "test-pyro", Name: "service_name"})
	require.NoError(t, err)
	assert.Equal(t, []string{}, values)
}

// testFlameFrame is the nested-set frame Grafana's Pyroscope datasource
// returns from /api/ds/query, with rows in depth-first order and an enum
// label field, for the call tree:
//
//	total (180)
//	+- main.entry (150) -> main.mid (150, self 50) -> main.mid (100, recursion) -> main.leaf (100)
//	+- other (30, server-side truncation bucket)
func testFlameFrame() *data.Frame {
	label := data.NewField("label", nil, []data.EnumItemIndex{0, 1, 2, 2, 3, 4})
	label.SetConfig(&data.FieldConfig{TypeConfig: &data.FieldTypeConfig{Enum: &data.EnumFieldConfig{
		Text: []string{"total", "main.entry", "main.mid", "main.leaf", "other"},
	}}})
	frame := data.NewFrame("response",
		data.NewField("level", nil, []int64{0, 1, 2, 3, 4, 1}),
		data.NewField("value", nil, []int64{180, 150, 150, 100, 100, 30}),
		data.NewField("self", nil, []int64{0, 0, 50, 0, 100, 30}),
		label,
	)
	frame.SetMeta(&data.FrameMeta{PreferredVisualization: data.VisTypeFlameGraph})
	return frame
}

func testFlameNodes(t *testing.T) []flameNode {
	t.Helper()
	nodes, err := flameFrameNodes(testFlameFrame())
	require.NoError(t, err)
	return nodes
}

func TestFlameFrameNodes_Int64Labels(t *testing.T) {
	frame := testFlameFrame()
	label := data.NewField("label", nil, []int64{0, 1, 2, 2, 3, 4})
	label.SetConfig(frame.Fields[3].Config)
	frame.Fields[3] = label
	nodes, err := flameFrameNodes(frame)
	require.NoError(t, err)
	assert.Equal(t, testFlameNodes(t), nodes)
}

func TestFlameFrameNodes_Invalid(t *testing.T) {
	_, err := flameFrameNodes(data.NewFrame("response", data.NewField("level", nil, []int64{0})))
	assert.ErrorContains(t, err, "want level, value, self and label fields")

	frame := testFlameFrame()
	frame.Fields[0] = data.NewField("level", nil, []int64{0, 2, 2, 3, 4, 1})
	_, err = flameFrameNodes(frame)
	assert.ErrorContains(t, err, "row 1 is at level 2")

	frame = testFlameFrame()
	frame.Fields[3] = data.NewField("label", nil, []string{"total", "a", "b", "c", "d", "e"})
	_, err = flameFrameNodes(frame)
	assert.ErrorContains(t, err, "label field has type string")
}

func TestSeriesFramesToResponse(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	series := func(pod string, values ...float64) *data.Frame {
		times := make([]time.Time, len(values))
		for i := range values {
			times[i] = start.Add(time.Duration(i) * time.Minute)
		}
		value := data.NewField("cpu", data.Labels{"pod": pod}, values)
		value.SetConfig(&data.FieldConfig{Unit: "cores"})
		return data.NewFrame("series", data.NewField("time", nil, times), value)
	}
	exemplars := data.NewFrame("exemplar", data.NewField("Time", nil, []time.Time{start}), data.NewField("Value", nil, []float64{9}))

	// The datasource raised the requested 5s step to 60s.
	res := seriesFramesToResponse(data.Frames{series("a", 0.5, 1.5), exemplars, series("empty")}, start, end, 5)

	assert.Equal(t, []rawSeries{{
		Labels: map[string]string{"pod": "a"},
		Points: [][2]float64{{float64(start.UnixMilli()), 0.5}, {float64(start.Add(time.Minute).UnixMilli()), 1.5}},
	}}, res.Series)
	assert.Equal(t, "cores", res.Unit)
	assert.InDelta(t, 60.0, res.StepSecs, 0.01)
	assert.Equal(t, start.Format(time.RFC3339), res.TimeRange["from"])

	empty := seriesFramesToResponse(nil, start, end, 5)
	assert.Equal(t, []rawSeries{}, empty.Series)
	assert.InDelta(t, 5.0, empty.StepSecs, 0.01)
}

// TestQueryPyroscope_DSQuery checks that profile and metrics queries go
// through /api/ds/query with millisecond bounds and the requested step.
func TestQueryPyroscope_DSQuery(t *testing.T) {
	var payloads []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasources/uid/test-pyro", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"uid": "test-pyro", "type": "grafana-pyroscope-datasource", "id": 1})
	})
	mux.HandleFunc("/api/ds/query", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		payloads = append(payloads, payload)
		frames := data.Frames{testFlameFrame()}
		if payload["queries"].([]any)[0].(map[string]any)["queryType"] == "metrics" {
			value := data.NewField("cpu", data.Labels{"pod": "a"}, []float64{0.25})
			value.SetConfig(&data.FieldConfig{Unit: "cores"})
			frames = data.Frames{data.NewFrame("series", data.NewField("time", nil, []time.Time{time.UnixMilli(1735689600000)}), value)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": backend.DataResponse{Frames: frames}}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	u, _ := url.Parse(ts.URL)
	cfg := client.DefaultTransportConfig()
	cfg.Host = u.Host
	cfg.Schemes = []string{"http"}
	ctx := mcpgrafana.WithGrafanaClient(
		mcpgrafana.WithGrafanaConfig(t.Context(), mcpgrafana.GrafanaConfig{URL: ts.URL}),
		&mcpgrafana.GrafanaClient{GrafanaHTTPAPI: client.NewHTTPClientWithConfig(nil, cfg)},
	)

	out, err := queryPyroscope(ctx, QueryPyroscopeParams{
		DataSourceUID: "test-pyro",
		ProfileType:   "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Matchers:      `service_name="web"`,
		GroupBy:       []string{"pod"},
		Step:          60,
		StartRFC3339:  "2025-01-01T00:00:00Z",
		EndRFC3339:    "2025-01-01T01:00:00Z",
	})
	require.NoError(t, err)

	var parsed struct {
		Profile string         `json:"profile"`
		Metrics seriesResponse `json:"metrics"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	assert.Contains(t, parsed.Profile, "main.leaf")
	assert.Equal(t, "cores", parsed.Metrics.Unit)
	require.Len(t, parsed.Metrics.Series, 1)
	assert.Equal(t, [2]float64{1735689600000, 0.25}, parsed.Metrics.Series[0].Points[0])

	require.Len(t, payloads, 2)
	for _, p := range payloads {
		assert.Equal(t, "1735689600000", p["from"])
		assert.Equal(t, "1735693200000", p["to"])
		q := p["queries"].([]any)[0].(map[string]any)
		assert.Equal(t, "process_cpu:cpu:nanoseconds:cpu:nanoseconds", q["profileTypeId"])
		assert.Equal(t, `{service_name="web"}`, q["labelSelector"])
		assert.Equal(t, map[string]any{"uid": "test-pyro", "type": "grafana-pyroscope-datasource"}, q["datasource"])
		if q["queryType"] == "metrics" {
			assert.Equal(t, []any{"pod"}, q["groupBy"])
			assert.Equal(t, 60000.0, q["intervalMs"])
			assert.Equal(t, 61.0, q["maxDataPoints"])
		} else {
			assert.Equal(t, "profile", q["queryType"])
			assert.NotContains(t, q, "maxNodes", "the table relies on Pyroscope's default node limit")
		}
	}
}
