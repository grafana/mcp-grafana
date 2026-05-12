//go:build unit

package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
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
	return b
}

func TestVictoriaLogsBackend_ListLabelNames(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"values":[{"value":"app","hits":12},{"value":"pod","hits":7}]}`)
	})
	b := newTestVLBackend(t, fake.server)

	names, err := b.ListLabelNames(context.Background(), time.Time{}, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "pod"}, names)
	assert.Equal(t, "/select/logsql/field_names", fake.lastPath)
	assert.Equal(t, http.MethodGet, fake.lastMethod)
	assert.Equal(t, victoriaLogsAllQuery, fake.lastQuery.Get("query"))
}

func TestVictoriaLogsBackend_ListLabelValues_PassesField(t *testing.T) {
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"values":[{"value":"acme-app","hits":5}]}`)
	})
	b := newTestVLBackend(t, fake.server)

	start := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	values, err := b.ListLabelValues(context.Background(), "app", start, end)
	require.NoError(t, err)
	assert.Equal(t, []string{"acme-app"}, values)
	assert.Equal(t, "app", fake.lastQuery.Get("field"))
	assert.Equal(t, start.Format(time.RFC3339), fake.lastQuery.Get("start"))
	assert.Equal(t, end.Format(time.RFC3339), fake.lastQuery.Get("end"))
}

func TestVictoriaLogsBackend_QueryLogs_ParsesNDJSON(t *testing.T) {
	body := strings.Join([]string{
		`{"_time":"2026-05-10T12:00:00Z","_stream":"{app=\"acme-app\",pod=\"p1\"}","_msg":"hello","level":"info"}`,
		`{"_time":"2026-05-10T12:00:01Z","_stream":"{app=\"acme-app\",pod=\"p1\"}","_msg":"world","level":"warn"}`,
	}, "\n")

	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	b := newTestVLBackend(t, fake.server)

	res, err := b.QueryLogs(context.Background(), lokiQueryParams{
		Query:     `{app="acme-app"}`,
		QueryType: "range",
		Start:     time.Date(2026, 5, 10, 11, 0, 0, 0, time.UTC),
		End:       time.Date(2026, 5, 10, 13, 0, 0, 0, time.UTC),
		Limit:     50,
		Direction: "backward",
	})
	require.NoError(t, err)
	assert.Equal(t, "/select/logsql/query", fake.lastPath)
	assert.Equal(t, http.MethodPost, fake.lastMethod)
	assert.Equal(t, `{app="acme-app"}`, fake.lastForm.Get("query"))
	assert.Equal(t, "50", fake.lastForm.Get("limit"))

	require.Equal(t, "streams", res.ResultType)
	require.Len(t, res.Entries, 2)
	first := res.Entries[0]
	assert.Equal(t, "hello", first.Line)
	assert.Equal(t, map[string]string{"app": "acme-app", "pod": "p1"}, first.Labels)
	assert.Equal(t, "info", first.Parsed["level"])
	// Timestamp should be normalized to nanoseconds-since-epoch.
	assert.NotEmpty(t, first.Timestamp)
	assert.NotContains(t, first.Timestamp, "T") // not RFC3339 anymore
}

func TestVictoriaLogsBackend_QueryLogs_ForwardReversesOrder(t *testing.T) {
	// VL returns newest-first by default; "forward" should reverse so the
	// caller sees oldest-first.
	body := strings.Join([]string{
		`{"_time":"2026-05-10T12:00:02Z","_msg":"newest"}`,
		`{"_time":"2026-05-10T12:00:01Z","_msg":"middle"}`,
		`{"_time":"2026-05-10T12:00:00Z","_msg":"oldest"}`,
	}, "\n")
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	b := newTestVLBackend(t, fake.server)

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
	fake := newFakeVLServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "")
	})
	b := newTestVLBackend(t, fake.server)

	end := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	_, err := b.QueryLogs(context.Background(), lokiQueryParams{
		Query:     "*",
		QueryType: "instant",
		End:       end,
		Limit:     1,
	})
	require.NoError(t, err)
	assert.Equal(t, end.Format(time.RFC3339), fake.lastForm.Get("start"))
	assert.Equal(t, end.Format(time.RFC3339), fake.lastForm.Get("end"))
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
	assert.Equal(t, 0, stats.Bytes)

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
