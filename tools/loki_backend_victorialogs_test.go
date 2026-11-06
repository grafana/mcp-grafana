//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeVLServer routes /select/logsql/* requests to canned responses and
// records the most recent request so tests can assert URL/body shape.
type fakeVLServer struct {
	server      *httptest.Server
	lastPath    string
	lastMethod  string
	lastQuery   url.Values
	lastForm    url.Values
	respHandler http.HandlerFunc
}

func newFakeVLServer(t *testing.T, handler http.HandlerFunc) *fakeVLServer {
	t.Helper()
	f := &fakeVLServer{respHandler: handler}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastPath = r.URL.Path
		f.lastMethod = r.Method
		f.lastQuery = r.URL.Query()
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			f.lastForm = r.PostForm
		}
		f.respHandler(w, r)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func newTestVLBackend(t *testing.T, server *httptest.Server) *victoriaLogsBackend {
	t.Helper()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: server.URL})
	b, err := newVictoriaLogsBackend(ctx, "vl-uid", nil)
	require.NoError(t, err)
	// The backend's baseURL targets /api/datasources/proxy/uid/{uid} on the
	// real Grafana; for tests we point it at the fake server root.
	b.baseURL = server.URL
	b.dsQueryURL = server.URL
	return b
}

func TestVictoriaLogsBackend_ListLabelNames(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"values":[{"value":"app","hits":12},{"value":"pod","hits":7}]}`)
	})
	b := newTestVLBackend(t, fake.server)

	names, err := b.ListLabelNames(context.Background(), "", time.Time{}, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "pod"}, names)
	assert.Equal(t, "/select/logsql/field_names", fake.lastPath)
	assert.Equal(t, http.MethodGet, fake.lastMethod)
	assert.Equal(t, victoriaLogsAllQuery, fake.lastQuery.Get("query"))
}

func TestVictoriaLogsBackend_ListLabelNames_PassesMatcher(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"values":[{"value":"service","hits":3}]}`)
	})
	b := newTestVLBackend(t, fake.server)

	names, err := b.ListLabelNames(context.Background(), `{namespace="prod"}`, time.Time{}, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, []string{"service"}, names)
	assert.Equal(t, `{namespace="prod"}`, fake.lastQuery.Get("query"))
}

func TestVictoriaLogsBackend_ListLabelValues_PassesField(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"values":[{"value":"acme-app","hits":5}]}`)
	})
	b := newTestVLBackend(t, fake.server)

	start := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	values, err := b.ListLabelValues(context.Background(), "app", "", start, end)
	require.NoError(t, err)
	assert.Equal(t, []string{"acme-app"}, values)
	assert.Equal(t, "app", fake.lastQuery.Get("field"))
	assert.Equal(t, victoriaLogsAllQuery, fake.lastQuery.Get("query"))
	assert.Equal(t, start.Format(time.RFC3339), fake.lastQuery.Get("start"))
	assert.Equal(t, end.Format(time.RFC3339), fake.lastQuery.Get("end"))
}

func TestVictoriaLogsBackend_ListLabelValues_PassesMatcher(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"values":[{"value":"acme-app","hits":5}]}`)
	})
	b := newTestVLBackend(t, fake.server)

	values, err := b.ListLabelValues(context.Background(), "app", `{namespace="prod"}`, time.Time{}, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, []string{"acme-app"}, values)
	assert.Equal(t, "app", fake.lastQuery.Get("field"))
	assert.Equal(t, `{namespace="prod"}`, fake.lastQuery.Get("query"))
}

// vlRow is one log record as the VictoriaLogs plugin reads it from
// /select/logsql/query: _time and _msg plus the remaining fields.
type vlRow struct {
	time   time.Time
	msg    string
	fields string // JSON object of the remaining fields, as the plugin keeps them
	stream map[string]string
}

// vlLogFrame builds a frame shaped like the one victorialogs-datasource
// v0.33 returns for log queries (pkg/plugin/response_logs.go newLogFrame).
func vlLogFrame(rows ...vlRow) *data.Frame {
	var (
		times                []time.Time
		lines, ids, streamID []string
		labels               []json.RawMessage
		streams              []*json.RawMessage
	)
	for i, r := range rows {
		times = append(times, r.time)
		lines = append(lines, r.msg)
		ids = append(ids, strconv.Itoa(i))
		labels = append(labels, json.RawMessage(r.fields))
		var s *json.RawMessage
		if r.stream != nil {
			b, _ := json.Marshal(r.stream)
			raw := json.RawMessage(b)
			s = &raw
		}
		streams = append(streams, s)
		streamID = append(streamID, "")
	}
	frame := data.NewFrame("",
		data.NewField("Time", nil, times),
		data.NewField("Line", nil, lines),
		data.NewField("id", nil, ids),
		data.NewField("labels", nil, labels),
		data.NewField("streams", nil, streams),
		data.NewField("streamId", nil, streamID),
	)
	frame.Meta = &data.FrameMeta{PreferredVisualization: "logs"}
	return frame
}

// newDSQueryVLServer answers /api/ds/query with the given response and
// records the payload it received.
func newDSQueryVLServer(t *testing.T, resp backend.DataResponse) (*httptest.Server, *map[string]interface{}) {
	t.Helper()
	payload := &map[string]interface{}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ds/query" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(payload)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": resp}})
	}))
	t.Cleanup(server.Close)
	return server, payload
}

func TestVictoriaLogsBackend_QueryLogs_DSQuery(t *testing.T) {
	stream := map[string]string{"app": "acme-app", "pod": "p1"}
	server, payload := newDSQueryVLServer(t, backend.DataResponse{Frames: data.Frames{vlLogFrame(
		vlRow{
			time:   time.Date(2026, 5, 10, 12, 0, 1, 123456789, time.UTC),
			msg:    "world",
			fields: `{"_stream":"{app=\"acme-app\",pod=\"p1\"}","_stream_id":"abc","level":"warn"}`,
			stream: stream,
		},
		vlRow{
			time:   time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC),
			msg:    "hello",
			fields: `{"_stream":"{app=\"acme-app\",pod=\"p1\"}","_stream_id":"abc","level":"info"}`,
			stream: stream,
		},
	)}})
	b := newTestVLBackend(t, server)

	res, err := b.QueryLogs(context.Background(), lokiQueryParams{
		Query:     `{app="acme-app"} error`,
		QueryType: "range",
		Start:     time.Date(2026, 5, 10, 11, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 5, 10, 13, 0, 0, 0, time.UTC),
		Limit:     50,
		Direction: "backward",
	})
	require.NoError(t, err)

	assert.Equal(t, "1778410800000", (*payload)["from"])
	assert.Equal(t, "1778418000000", (*payload)["to"])
	queries := (*payload)["queries"].([]interface{})
	require.Len(t, queries, 1)
	assert.Equal(t, map[string]interface{}{
		"refId":      "A",
		"datasource": map[string]interface{}{"uid": "vl-uid", "type": victoriaLogsDatasourceType},
		"expr":       `{app="acme-app"} error`,
		"queryType":  "instant",
		"maxLines":   float64(50),
	}, queries[0])

	require.Equal(t, "streams", res.ResultType)
	assert.Equal(t, []LogEntry{
		{
			Timestamp: "1778414401123456789",
			Line:      "world",
			Labels:    stream,
			Parsed:    map[string]string{"_stream_id": "abc", "level": "warn"},
		},
		{
			Timestamp: "1778414400000000000",
			Line:      "hello",
			Labels:    stream,
			Parsed:    map[string]string{"_stream_id": "abc", "level": "info"},
		},
	}, res.Entries)
}

func TestVictoriaLogsBackend_QueryLogs_OlderPluginFrames(t *testing.T) {
	ts := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	t.Run("v0.29 drops _stream from labels and adds streams", func(t *testing.T) {
		server, _ := newDSQueryVLServer(t, backend.DataResponse{Frames: data.Frames{vlLogFrame(vlRow{
			time: ts, msg: "hello", fields: `{"level":"info"}`, stream: map[string]string{"app": "a"},
		})}})
		res, err := newTestVLBackend(t, server).QueryLogs(context.Background(), lokiQueryParams{Query: "*", Start: ts, End: ts.Add(time.Second)})
		require.NoError(t, err)
		require.Len(t, res.Entries, 1)
		assert.Equal(t, map[string]string{"app": "a"}, res.Entries[0].Labels)
		assert.Equal(t, map[string]string{"level": "info"}, res.Entries[0].Parsed)
	})
	t.Run("before v0.29 flattens everything into labels", func(t *testing.T) {
		// Time, Line and labels only (pkg/plugin/response.go in v0.28.0).
		frame := data.NewFrame("",
			data.NewField("Time", nil, []time.Time{ts}),
			data.NewField("Line", nil, []string{"hello"}),
			data.NewField("labels", nil, []json.RawMessage{json.RawMessage(`{"app":"a","level":"info"}`)}),
		)
		server, _ := newDSQueryVLServer(t, backend.DataResponse{Frames: data.Frames{frame}})
		res, err := newTestVLBackend(t, server).QueryLogs(context.Background(), lokiQueryParams{Query: "*", Start: ts, End: ts.Add(time.Second)})
		require.NoError(t, err)
		require.Len(t, res.Entries, 1)
		assert.Equal(t, map[string]string{"app": "a", "level": "info"}, res.Entries[0].Labels)
		assert.Nil(t, res.Entries[0].Parsed)
	})
}

func TestVictoriaLogsBackend_QueryLogs_TrimsToNanosecondBounds(t *testing.T) {
	start := time.Date(2026, 5, 10, 12, 0, 0, 500, time.UTC)
	end := time.Date(2026, 5, 10, 12, 0, 1, 999999000, time.UTC)
	server, payload := newDSQueryVLServer(t, backend.DataResponse{Frames: data.Frames{vlLogFrame(
		vlRow{time: end, msg: "at end", fields: `{}`},
		vlRow{time: end.Add(-time.Nanosecond), msg: "last", fields: `{}`},
		vlRow{time: start, msg: "first", fields: `{}`},
		vlRow{time: start.Add(-time.Nanosecond), msg: "before start", fields: `{}`},
	)}})
	b := newTestVLBackend(t, server)

	res, err := b.QueryLogs(context.Background(), lokiQueryParams{Query: "*", Start: start, End: end})
	require.NoError(t, err)
	// The range is widened to whole milliseconds...
	assert.Equal(t, strconv.FormatInt(start.Truncate(time.Millisecond).UnixMilli(), 10), (*payload)["from"])
	assert.Equal(t, strconv.FormatInt(end.Truncate(time.Millisecond).Add(time.Millisecond).UnixMilli(), 10), (*payload)["to"])
	// ...and trimmed back to [start, end).
	require.Len(t, res.Entries, 2)
	assert.Equal(t, "last", res.Entries[0].Line)
	assert.Equal(t, "first", res.Entries[1].Line)
}

func TestVictoriaLogsBackend_QueryLogs_ForwardReversesOrder(t *testing.T) {
	// VL returns newest-first for limited queries; "forward" should reverse
	// so the caller sees oldest-first.
	ts := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	server, _ := newDSQueryVLServer(t, backend.DataResponse{Frames: data.Frames{vlLogFrame(
		vlRow{time: ts.Add(2 * time.Second), msg: "newest", fields: `{}`},
		vlRow{time: ts.Add(time.Second), msg: "middle", fields: `{}`},
		vlRow{time: ts, msg: "oldest", fields: `{}`},
	)}})
	b := newTestVLBackend(t, server)

	res, err := b.QueryLogs(context.Background(), lokiQueryParams{
		Query:     "*",
		QueryType: "range",
		Direction: "forward",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, res.Entries, 3)
	assert.Equal(t, "oldest", res.Entries[0].Line)
	assert.Equal(t, "newest", res.Entries[2].Line)
}

func TestVictoriaLogsBackend_QueryLogs_InstantCollapsesWindow(t *testing.T) {
	server, payload := newDSQueryVLServer(t, backend.DataResponse{Frames: data.Frames{vlLogFrame()}})
	b := newTestVLBackend(t, server)

	end := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	_, err := b.QueryLogs(context.Background(), lokiQueryParams{
		Query:     "*",
		QueryType: "instant",
		End:       end,
		Limit:     1,
	})
	require.NoError(t, err)
	assert.Equal(t, strconv.FormatInt(end.UnixMilli(), 10), (*payload)["from"])
	assert.Equal(t, strconv.FormatInt(end.UnixMilli(), 10), (*payload)["to"])
}

func TestVictoriaLogsBackend_QueryLogs_DSQueryError(t *testing.T) {
	// The plugin wraps VictoriaLogs' 400 body (pkg/plugin/response.go parseStringResponseError).
	server, _ := newDSQueryVLServer(t, backend.DataResponse{
		Error:  errors.New("error from datasource: cannot parse query"),
		Status: backend.StatusInternal,
	})
	b := newTestVLBackend(t, server)

	_, err := b.QueryLogs(context.Background(), lokiQueryParams{Query: "|", Start: time.Now().Add(-time.Hour), End: time.Now()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot parse query")
}

func TestVictoriaLogsBackend_QueryLogs_RefillsTrimmedLeadingEdge(t *testing.T) {
	// end is mid-millisecond, so the widened range admits lines past end.
	// VictoriaLogs keeps the newest maxLines lines, so those lines use up
	// the first page and the backend asks again with limit plus the number
	// of trimmed lines, until the page is full.
	start := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 10, 12, 0, 1, 500, time.UTC)
	rows := []vlRow{
		{time: end.Add(300), msg: "past end 1", fields: `{}`},
		{time: end.Add(200), msg: "past end 2", fields: `{}`},
		{time: end, msg: "past end 3", fields: `{}`},
		{time: end.Add(-time.Second / 2), msg: "in range 1", fields: `{}`},
		{time: end.Add(-time.Second), msg: "in range 2", fields: `{}`},
		{time: end.Add(-2 * time.Second), msg: "in range 3", fields: `{}`},
		{time: start.Add(-time.Nanosecond), msg: "never sent", fields: `{}`},
	}
	var maxLines []float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Queries []map[string]interface{} `json:"queries"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		n := payload.Queries[0]["maxLines"].(float64)
		maxLines = append(maxLines, n)
		frame := vlLogFrame(rows[:min(int(n), len(rows)-1)]...)
		_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": {Frames: data.Frames{frame}}}})
	}))
	t.Cleanup(server.Close)

	res, err := newTestVLBackend(t, server).QueryLogs(context.Background(), lokiQueryParams{Query: "*", Start: start, End: end, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []float64{2, 4, 5}, maxLines)
	require.Len(t, res.Entries, 2)
	assert.Equal(t, "in range 1", res.Entries[0].Line)
	assert.Equal(t, "in range 2", res.Entries[1].Line)
}

func TestVictoriaLogsBackend_QueryStats_AppendsCountPipe(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"__name__":"entries"},"value":[1700000000,"42"]}]}}`)
	})
	b := newTestVLBackend(t, fake.server)

	stats, err := b.QueryStats(context.Background(), `{app="acme-app"}`, time.Time{}, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 42, stats.Entries)
	// Other fields should remain zero — VL does not expose them.
	assert.Equal(t, 0, stats.Streams)
	assert.Equal(t, 0, stats.Chunks)
	assert.Equal(t, int64(0), stats.Bytes)

	// Confirm we appended a stats pipe.
	q := fake.lastForm.Get("query")
	assert.Contains(t, q, "| stats count(*) as entries")
}

func TestVictoriaLogsBackend_QueryStats_PreservesUserStatsPipe(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})
	b := newTestVLBackend(t, fake.server)

	custom := `{app="x"} | stats count() as my_count`
	_, err := b.QueryStats(context.Background(), custom, time.Time{}, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, custom, fake.lastForm.Get("query"))
}

func TestVictoriaLogsBackend_QueryPatterns_NotSupported(t *testing.T) {
	// The backend should refuse without making a request.
	called := false
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	b := newTestVLBackend(t, fake.server)

	_, err := b.QueryPatterns(context.Background(), `{job="x"}`, "", time.Time{}, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VictoriaLogs")
	assert.False(t, called)
}

func TestParseVLStream_QuotedCommas(t *testing.T) {
	got := parseVLStream(`{app="acme-app",path="/foo,bar",pod="p1"}`)
	assert.Equal(t, map[string]string{
		"app":  "acme-app",
		"path": "/foo,bar",
		"pod":  "p1",
	}, got)
}

func TestVLTimeToNanos_Fallback(t *testing.T) {
	// Valid RFC3339 → epoch nanos.
	assert.Equal(t, "1778414400000000000", vlTimeToNanos("2026-05-10T12:00:00Z"))
	// Unparseable → returned verbatim.
	assert.Equal(t, "garbage", vlTimeToNanos("garbage"))
	assert.Equal(t, "", vlTimeToNanos(""))
}
