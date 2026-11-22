//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var causeEnd = time.Date(2026, 2, 2, 20, 0, 0, 0, time.UTC)

// promCauseServer serves label lookups from a fixed metric -> label names map
// and records each request path.
func promCauseServer(t *testing.T, metrics map[string][]string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.NoError(t, r.ParseForm())
		matchers, err := promqlParser.ParseMetricSelector(r.Form.Get("match[]"))
		require.NoError(t, err)
		var out []string
		for name, labelNames := range metrics {
			if !matchers[0].Matches(name) {
				continue
			}
			switch {
			case strings.HasSuffix(r.URL.Path, "/api/v1/label/__name__/values"):
				out = append(out, name)
			case strings.HasSuffix(r.URL.Path, "/api/v1/labels"):
				out = append(out, labelNames...)
			}
		}
		slices.Sort(out)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": out})
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestPromEmptyCause(t *testing.T) {
	metrics := map[string][]string{
		"latency_seconds":     {"__name__", "job"},
		"http_requests_total": {"__name__", "job"},
		"up":                  {"__name__", "instance", "job"},
	}
	start := causeEnd.Add(-time.Hour)
	for _, tc := range []struct {
		name, expr, reason string
		want               []string
		lookups            int
	}{
		{"native histogram count", `rate(latency_seconds_count[5m])`, ReasonNativeHistogram, []string{"latency_seconds is a native histogram", "histogram_count()"}, 1},
		{"native histogram bucket", `histogram_quantile(0.9, rate(latency_seconds_bucket[5m]))`, ReasonNativeHistogram, []string{"histogram_quantile()"}, 1},
		{"missing _total suffix", `rate(http_requests[5m])`, ReasonMetricNotFound, []string{"but http_requests_total does", "Query http_requests_total"}, 1},
		{"metric not found", `nope_metric`, ReasonMetricNotFound, []string{"nope_metric has no series", "list_prometheus_metric_names"}, 1},
		{"second selector missing", `up / nope_metric`, ReasonMetricNotFound, []string{"nope_metric"}, 1},
		{"label not found", `up{jbo="api"}`, ReasonLabelNotFound, []string{"up has no label jbo", "instance, job."}, 2},
		{"label value matches nothing", `up{job="nope"}`, ReasonNoMatchingSeries, []string{"list_prometheus_label_values"}, 2},
		{"matcher accepting empty skips label lookup", `up{job!="api"}`, ReasonNoMatchingSeries, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, paths := promCauseServer(t, metrics)
			cause := promEmptyCause(context.Background(), newTestPrometheusBackend(t, srv), tc.expr, start, causeEnd, 60)
			require.NotNil(t, cause)
			assert.Equal(t, tc.reason, cause.Reason)
			for _, s := range tc.want {
				assert.Contains(t, cause.Summary+" "+cause.Action, s)
			}
			assert.Len(t, *paths, tc.lookups)
		})
	}
}

func TestPromEmptyCauseInstantWindow(t *testing.T) {
	var gotStart string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotStart = r.Form.Get("start")
		_, _ = io.WriteString(w, `{"status":"success","data":[]}`)
	}))
	defer srv.Close()

	b := newTestPrometheusBackend(t, srv)
	promEmptyCause(context.Background(), b, `rate(up[1h])`, time.Time{}, causeEnd, 0)
	assert.Equal(t, "1770058800", gotStart, "range selector [1h] sets the lookback")
	promEmptyCause(context.Background(), b, `up`, time.Time{}, causeEnd, 0)
	assert.Equal(t, "1770062100", gotStart, "default 5m lookback delta")
}

func TestPromEmptyCauseUnknown(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	start := causeEnd.Add(-time.Hour)

	t.Run("lookup fails", func(t *testing.T) {
		assert.Nil(t, promEmptyCause(context.Background(), newTestPrometheusBackend(t, failing), `up`, start, causeEnd, 60))
	})
	t.Run("label lookup fails", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/api/v1/labels") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, `{"status":"success","data":["up"]}`)
		}))
		defer srv.Close()
		assert.Nil(t, promEmptyCause(context.Background(), newTestPrometheusBackend(t, srv), `up{job="api"}`, start, causeEnd, 60))
	})
	t.Run("no metric name makes no request", func(t *testing.T) {
		srv, paths := promCauseServer(t, nil)
		assert.Nil(t, promEmptyCause(context.Background(), newTestPrometheusBackend(t, srv), `{job="api"}`, start, causeEnd, 60))
		assert.Empty(t, *paths)
	})
	t.Run("non-Prometheus backend", func(t *testing.T) {
		assert.Nil(t, promEmptyCause(context.Background(), &cloudMonitoringBackend{}, `up`, start, causeEnd, 60))
	})
}

// lokiStatsBackend answers index/stats with streams for windows that start at
// or after recentFrom and with zero streams otherwise.
func lokiStatsBackend(t *testing.T, recentFrom time.Time, calls *int) *lokiNativeBackend {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		assert.Equal(t, `{app="api"}`, r.URL.Query().Get("query"))
		from, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
		require.NoError(t, err)
		streams := 0
		if from >= recentFrom.UnixNano() {
			streams = 2
		}
		_ = json.NewEncoder(w).Encode(Stats{Streams: streams})
	}))
	t.Cleanup(srv.Close)
	return &lokiNativeBackend{client: &Client{httpClient: srv.Client(), baseURL: srv.URL}}
}

func TestLokiEmptyCause(t *testing.T) {
	now := causeEnd
	recent := now.Add(-time.Hour)
	old := now.Add(-30 * 24 * time.Hour)
	scanned := func(n int) *int { return &n }
	for _, tc := range []struct {
		name       string
		logql      string
		start, end time.Time
		dataFrom   time.Time
		scanned    *int
		reason     string
		calls      int
	}{
		{"lines scanned, pipeline", `{app="api"} |= "panic"`, recent, now, recent, scanned(120), ReasonLinesFiltered, 0},
		{"streams exist, pipeline", `{app="api"} | json | status="500"`, recent, now, recent, nil, ReasonLinesFiltered, 1},
		{"metric query with filter", `sum(count_over_time({app="api"} |= "x" [5m]))`, recent, now, recent, scanned(0), ReasonLinesFiltered, 1},
		{"no streams, recent range", `{app="api"}`, recent, now, now.Add(time.Hour), nil, ReasonNoMatchingStreams, 1},
		{"old range, recent data", `{app="api"}`, old, old.Add(time.Hour), recent, nil, ReasonNoDataInRange, 2},
		{"old range, no data anywhere", `{app="api"}`, old, old.Add(time.Hour), now.Add(time.Hour), nil, ReasonNoMatchingStreams, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			b := lokiStatsBackend(t, tc.dataFrom, &calls)
			cause := lokiEmptyCause(context.Background(), b, tc.logql, tc.start, tc.end, now, tc.scanned)
			require.NotNil(t, cause)
			assert.Equal(t, tc.reason, cause.Reason)
			assert.Equal(t, tc.calls, calls)
		})
	}
}

func TestLokiEmptyCauseUnknown(t *testing.T) {
	now := causeEnd
	start := now.Add(-time.Hour)
	for _, tc := range []struct {
		name, logql string
		scanned     int
	}{
		{"two selectors", `{app="api"} or {app="web"}`, 0},
		{"lines scanned, no pipeline", `{app="api"}`, 5},
		{"streams exist, no pipeline", `{app="api"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			b := lokiStatsBackend(t, start, &calls)
			assert.Nil(t, lokiEmptyCause(context.Background(), b, tc.logql, start, now, now, &tc.scanned))
		})
	}
	t.Run("VictoriaLogs", func(t *testing.T) {
		assert.Nil(t, lokiEmptyCause(context.Background(), &victoriaLogsBackend{}, `{app="api"}`, start, now, now, nil))
	})
}

// The detected cause replaces the generic lists; the tool output carries the reason.
func TestQueryPrometheusEmptyResultReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/datasources/uid/prom":
			_, _ = io.WriteString(w, `{"uid":"prom","type":"prometheus"}`)
		case r.URL.Path == "/api/ds/query":
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": backend.DataResponse{}}})
		case strings.HasSuffix(r.URL.Path, "/api/v1/label/__name__/values"):
			_, _ = io.WriteString(w, `{"status":"success","data":["latency_seconds"]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	result, err := queryPrometheusWithHints(mockDatasourcesCtx(srv), QueryPrometheusParams{
		DatasourceUID: "prom", Expr: `rate(latency_seconds_count[5m])`, QueryType: "instant", EndTime: "now",
	})
	require.NoError(t, err)
	out, err := json.Marshal(result.Hints)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"reason": "native_histogram",
		"summary": "latency_seconds is a native histogram, so latency_seconds_count has no series.",
		"possibleCauses": [],
		"suggestedActions": ["Use histogram_count() on latency_seconds instead."]
	}`, string(out))
}

func TestQueryLokiLogsEmptyResultReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case r.URL.Path == "/api/ds/query":
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": backend.DataResponse{}}})
		case strings.HasSuffix(r.URL.Path, "/loki/api/v1/index/stats"):
			_, _ = io.WriteString(w, `{"streams":0}`)
		case r.URL.Path == "/api/frontend/settings":
			http.NotFound(w, r) // the Loki client probes for legacy routes
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	result, err := queryLokiLogs(enforceTestCtx(srv, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="nope"}`,
		StartRFC3339: time.Now().Add(-time.Hour).Format(time.RFC3339), EndRFC3339: time.Now().Format(time.RFC3339),
	})
	require.NoError(t, err)
	require.NotNil(t, result.Hints)
	assert.Equal(t, ReasonNoMatchingStreams, result.Hints.Reason)
	assert.Equal(t, []string{"Check labels with list_loki_label_names and list_loki_label_values."}, result.Hints.SuggestedActions)
}

// Regression: with no detected cause the generic fallback is unchanged and has no reason.
func TestEmptyResultHintsFallbackWithoutCause(t *testing.T) {
	for _, ds := range []string{"prometheus", "loki"} {
		hints := GenerateEmptyResultHints(HintContext{DatasourceType: ds, Query: "up"})
		assert.Empty(t, hints.Reason)
		assert.NotEmpty(t, hints.PossibleCauses)
		out, err := json.Marshal(hints)
		require.NoError(t, err)
		assert.NotContains(t, string(out), `"reason"`)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/prom":
			_, _ = io.WriteString(w, `{"uid":"prom","type":"prometheus"}`)
		case "/api/ds/query":
			_ = json.NewEncoder(w).Encode(backend.QueryDataResponse{Responses: backend.Responses{"A": backend.DataResponse{}}})
		default:
			w.WriteHeader(http.StatusInternalServerError) // lookups fail
		}
	}))
	defer srv.Close()
	result, err := queryPrometheusWithHints(mockDatasourcesCtx(srv), QueryPrometheusParams{
		DatasourceUID: "prom", Expr: `up`, QueryType: "instant", EndTime: "now",
	})
	require.NoError(t, err)
	assert.Equal(t, GenerateEmptyResultHints(HintContext{DatasourceType: "prometheus", Query: "up"}).PossibleCauses, result.Hints.PossibleCauses)
	assert.Empty(t, result.Hints.Reason)
}
