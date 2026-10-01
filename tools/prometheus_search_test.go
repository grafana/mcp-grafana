//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchPrometheusQueryParams(t *testing.T) {
	zero, over := 0, 101
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	t.Run("valid", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			args   PrometheusSearchParams
			search []string
			want   url.Values
		}{
			{
				name:   "defaults with search",
				search: []string{"http", "grpc"},
				want: url.Values{
					"search[]":       {"http", "grpc"},
					"fuzz_alg":       {"jarowinkler"},
					"fuzz_threshold": {"70"},
					"case_sensitive": {"false"},
					"sort_by":        {"score"},
					"limit":          {"50"},
				},
			},
			{
				name: "defaults without search sort alpha",
				want: url.Values{
					"fuzz_alg":       {"jarowinkler"},
					"fuzz_threshold": {"70"},
					"case_sensitive": {"false"},
					"sort_by":        {"alpha"},
					"limit":          {"50"},
				},
			},
			{
				name: "every option set",
				args: PrometheusSearchParams{
					Matches:       []string{`{job="api"}`, `http_requests_total`},
					StartRFC3339:  start.Format(time.RFC3339),
					EndRFC3339:    end.Format(time.RFC3339),
					FuzzAlg:       "subsequence",
					FuzzThreshold: &zero,
					SortBy:        "alpha",
					SortDir:       "dsc",
					Limit:         5,
					IncludeScore:  true,
				},
				want: url.Values{
					"match[]":        {`{job="api"}`, `http_requests_total`},
					"start":          {strconv.FormatInt(start.Unix(), 10)},
					"end":            {strconv.FormatInt(end.Unix(), 10)},
					"fuzz_alg":       {"subsequence"},
					"fuzz_threshold": {"0"},
					"case_sensitive": {"false"},
					"sort_by":        {"alpha"},
					"sort_dir":       {"dsc"},
					"limit":          {"5"},
					"include_score":  {"true"},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := searchPrometheusQueryParams(tc.args, tc.search)
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		a := []string{"a"}
		for _, tc := range []struct {
			name, wantErr string
			args          PrometheusSearchParams
			search        []string
		}{
			{"blank search term", "search terms must not be empty", PrometheusSearchParams{}, []string{" "}},
			{"bad selector", `invalid matches selector "{job="`, PrometheusSearchParams{Matches: []string{`{job=`}}, nil},
			{"bad start", "parsing start time", PrometheusSearchParams{StartRFC3339: "yesterday"}, a},
			{"start after end", "start time must not be after end time", PrometheusSearchParams{StartRFC3339: "now", EndRFC3339: "now-1h"}, a},
			{"bad fuzzAlg", `invalid fuzzAlg "levenshtein"`, PrometheusSearchParams{FuzzAlg: "levenshtein"}, a},
			{"fuzzThreshold too high", "invalid fuzzThreshold 101", PrometheusSearchParams{FuzzThreshold: &over}, a},
			{"score without search", "sortBy score requires search", PrometheusSearchParams{SortBy: "score"}, nil},
			{"bad sortBy", `invalid sortBy "size"`, PrometheusSearchParams{SortBy: "size"}, a},
			{"sortDir with score", "sortDir is valid with sortBy alpha only", PrometheusSearchParams{SortDir: "asc"}, a},
			{"bad sortDir", `invalid sortDir "desc"`, PrometheusSearchParams{SortDir: "desc"}, nil},
			{"negative limit", "invalid limit -1", PrometheusSearchParams{Limit: -1}, a},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := searchPrometheusQueryParams(tc.args, tc.search)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			})
		}
	})
}

func TestDecodeSearchStream(t *testing.T) {
	score := 0.0

	t.Run("batches and trailer", func(t *testing.T) {
		body := `{"results":[{"name":"a","score":0},{"name":"b"}],"warnings":["w1"]}
{"results":[{"name":"c","type":"counter","help":"h","unit":"seconds"}]}
{"status":"success","has_more":true,"warnings":["w1","w2"],"unknown":1}
`
		got, err := decodeSearchStream(context.Background(), strings.NewReader(body), defaultResponseLimitBytes, "metric_names")
		require.NoError(t, err)
		assert.Equal(t, &SearchPrometheusResponse{
			Results: []PrometheusSearchResult{
				{Name: "a", Score: &score},
				{Name: "b"},
				{Name: "c", Type: "counter", Help: "h", Unit: "seconds"},
			},
			HasMore:  true,
			Warnings: []string{"w1", "w2"},
		}, got)
	})

	t.Run("empty result encodes as an empty array", func(t *testing.T) {
		got, err := decodeSearchStream(context.Background(), strings.NewReader(`{"status":"success","has_more":false}`), defaultResponseLimitBytes, "label_names")
		require.NoError(t, err)
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.JSONEq(t, `{"results":[],"hasMore":false}`, string(raw))
	})

	t.Run("no trailer is incomplete", func(t *testing.T) {
		got, err := decodeSearchStream(context.Background(), strings.NewReader(`{"results":[{"value":"x"}]}`+"\n"), defaultResponseLimitBytes, "label_values")
		require.NoError(t, err)
		assert.Equal(t, []PrometheusSearchResult{{Value: "x"}}, got.Results)
		assert.True(t, got.HasMore)
		assert.True(t, got.Incomplete)
		require.Len(t, got.Warnings, 1)
		assert.Contains(t, got.Warnings[0], "without a trailer")
	})

	t.Run("size limit cuts the stream", func(t *testing.T) {
		body := `{"results":[{"name":"aaaa"}]}` + "\n" + `{"results":[{"name":"bbbb"}]}` + "\n" + `{"status":"success","has_more":false}`
		maxBytes := int64(len(`{"results":[{"name":"aaaa"}]}`) + 5)
		got, err := decodeSearchStream(context.Background(), strings.NewReader(body), maxBytes, "metric_names")
		require.NoError(t, err)
		assert.Equal(t, []PrometheusSearchResult{{Name: "aaaa"}}, got.Results)
		assert.True(t, got.Incomplete)
		require.Len(t, got.Warnings, 1)
		assert.Contains(t, got.Warnings[0], "exceeded")
	})

	t.Run("body of exactly the limit is complete", func(t *testing.T) {
		body := `{"status":"success","has_more":false}`
		got, err := decodeSearchStream(context.Background(), strings.NewReader(body), int64(len(body)), "metric_names")
		require.NoError(t, err)
		assert.False(t, got.Incomplete)
		assert.Empty(t, got.Warnings)
	})

	t.Run("error trailer", func(t *testing.T) {
		body := `{"results":[{"name":"a"}]}` + "\n" + `{"status":"error","errorType":"timeout","error":"query timed out"}`
		_, err := decodeSearchStream(context.Background(), strings.NewReader(body), defaultResponseLimitBytes, "metric_names")
		require.Error(t, err)
		assert.Equal(t, "searching Prometheus metric_names: timeout: query timed out", err.Error())
	})

	t.Run("malformed frame", func(t *testing.T) {
		_, err := decodeSearchStream(context.Background(), strings.NewReader(`{"results":[}`), defaultResponseLimitBytes, "metric_names")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parsing search response")
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := decodeSearchStream(ctx, strings.NewReader(`{"results":[{"name":"a"}]}`), defaultResponseLimitBytes, "metric_names")
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestSearchHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		wantHint         bool
	}{
		{
			name:     "mimir feature not enabled",
			status:   http.StatusNotFound,
			body:     `{"status":"error","errorType":"feature_not_enabled","error":"the experimental search API is not enabled"}`,
			want:     "prometheus search API returned status 404: feature_not_enabled: the experimental search API is not enabled",
			wantHint: true,
		},
		{
			name:     "prometheus search API disabled",
			status:   http.StatusServiceUnavailable,
			body:     `{"status":"error","errorType":"unavailable","error":"search API disabled"}`,
			want:     "prometheus search API returned status 503: unavailable: search API disabled",
			wantHint: true,
		},
		{
			name:   "bare 404 from a server without the route",
			status: http.StatusNotFound,
			body:   "404 page not found\n",
			want:   "prometheus search API returned status 404: 404 page not found",
		},
		{
			name:   "bad data",
			status: http.StatusBadRequest,
			body:   `{"status":"error","errorType":"bad_data","error":"invalid limit"}`,
			want:   "prometheus search API returned status 400: bad_data: invalid limit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := searchHTTPError(tc.status, []byte(tc.body))
			assert.True(t, strings.HasPrefix(err.Error(), tc.want), err.Error())
			assert.Equal(t, tc.wantHint, strings.Contains(err.Error(), "--enable-feature=search-api"))
		})
	}
}

// newSearchTestServer serves the datasource lookup for uid and passes other
// requests to handler.
func newSearchTestServer(t *testing.T, uid, dsType string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources/uid/"+uid {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&models.DataSource{UID: uid, Type: dsType})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// searchRequestRecorder answers every search request with an empty result
// and records the last request.
type searchRequestRecorder struct {
	path  string
	query url.Values
}

func (r *searchRequestRecorder) handler(w http.ResponseWriter, req *http.Request) {
	r.path, r.query = req.URL.Path, req.URL.Query()
	w.Header().Set("Content-Type", "application/x-ndjson")
	_, _ = w.Write([]byte(`{"status":"success","has_more":false}` + "\n"))
}

func TestSearchPrometheusTools(t *testing.T) {
	t.Run("metric names sends search and metadata", func(t *testing.T) {
		rec := &searchRequestRecorder{}
		server := newSearchTestServer(t, "prom-search-mn", "prometheus", rec.handler)

		_, err := searchPrometheusMetricNames(mockDatasourcesCtx(server), SearchPrometheusMetricNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "prom-search-mn"},
			Search:                 []string{"http"},
			IncludeMetadata:        true,
		})
		require.NoError(t, err)
		assert.Equal(t, "/api/datasources/uid/prom-search-mn/resources/api/v1/search/metric_names", rec.path)
		assert.Equal(t, []string{"http"}, rec.query["search[]"])
		assert.Equal(t, "true", rec.query.Get("include_metadata"))
	})

	t.Run("label names with matches only", func(t *testing.T) {
		rec := &searchRequestRecorder{}
		server := newSearchTestServer(t, "prom-search-ln", "prometheus", rec.handler)

		_, err := searchPrometheusLabelNames(mockDatasourcesCtx(server), SearchPrometheusLabelNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "prom-search-ln", Matches: []string{"up"}},
		})
		require.NoError(t, err)
		assert.Equal(t, "/api/datasources/uid/prom-search-ln/resources/api/v1/search/label_names", rec.path)
		assert.Equal(t, []string{"up"}, rec.query["match[]"])
		assert.Equal(t, "alpha", rec.query.Get("sort_by"))
		assert.Empty(t, rec.query.Get("include_metadata"))
	})

	t.Run("label values sends label and decodes values", func(t *testing.T) {
		server := newSearchTestServer(t, "prom-search-lv", "prometheus", func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/api/datasources/uid/prom-search-lv/resources/api/v1/search/label_values", r.URL.Path)
			assert.Equal(t, "job", r.URL.Query().Get("label"))
			assert.Equal(t, []string{"api"}, r.URL.Query()["search[]"])
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"results":[{"value":"api-server"}]}` + "\n" + `{"status":"success","has_more":false}` + "\n"))
		})

		got, err := searchPrometheusLabelValues(mockDatasourcesCtx(server), SearchPrometheusLabelValuesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "prom-search-lv"},
			Label:                  "job",
			Search:                 []string{"api"},
		})
		require.NoError(t, err)
		assert.Equal(t, &SearchPrometheusResponse{Results: []PrometheusSearchResult{{Value: "api-server"}}}, got)
	})

	t.Run("required inputs fail before any datasource call", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request to %s", r.URL.Path)
		}))
		t.Cleanup(server.Close)
		ctx := mockDatasourcesCtx(server)
		common := PrometheusSearchParams{DatasourceUID: "x"}

		_, err := searchPrometheusMetricNames(ctx, SearchPrometheusMetricNamesParams{PrometheusSearchParams: common})
		assert.ErrorContains(t, err, "search is required")

		_, err = searchPrometheusLabelNames(ctx, SearchPrometheusLabelNamesParams{PrometheusSearchParams: common})
		assert.ErrorContains(t, err, "give search, matches or both")

		_, err = searchPrometheusLabelValues(ctx, SearchPrometheusLabelValuesParams{PrometheusSearchParams: common, Label: " "})
		assert.ErrorContains(t, err, "label is required")

		_, err = searchPrometheusLabelValues(ctx, SearchPrometheusLabelValuesParams{PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "x", Limit: -1}, Label: "job"})
		assert.ErrorContains(t, err, "invalid limit -1")
	})

	t.Run("disabled API hint survives the fallback retry", func(t *testing.T) {
		var paths []string
		server := newSearchTestServer(t, "prom-search-disabled", "prometheus", func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":"error","errorType":"unavailable","error":"search API disabled"}`))
		})

		_, err := searchPrometheusMetricNames(mockDatasourcesCtx(server), SearchPrometheusMetricNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "prom-search-disabled"},
			Search:                 []string{"http"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--enable-feature=search-api")
		assert.Equal(t, []string{
			"/api/datasources/uid/prom-search-disabled/resources/api/v1/search/metric_names",
			"/api/datasources/proxy/uid/prom-search-disabled/api/v1/search/metric_names",
		}, paths)
	})

	t.Run("victoriametrics is rejected", func(t *testing.T) {
		server := newSearchTestServer(t, "vm-search", victoriaMetricsDatasourceType, func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request to %s", r.URL.Path)
		})

		_, err := searchPrometheusLabelNames(mockDatasourcesCtx(server), SearchPrometheusLabelNamesParams{
			PrometheusSearchParams: PrometheusSearchParams{DatasourceUID: "vm-search"},
			Search:                 []string{"job"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not support the Prometheus search API")
	})
}
