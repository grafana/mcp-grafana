//go:build unit

package tools

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListPrometheusMetricNamesDiscovery(t *testing.T) {
	names := []string{"a_cpu", "b_http_requests", "c_http_errors", "d_http_requests", "e_memory", "f_up", "g_up", "h_up", "i_up", "j_up", "k_up"}
	for _, datasourceType := range []string{"prometheus", victoriaMetricsDatasourceType, cloudMonitoringDatasourceType} {
		t.Run(datasourceType, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				regex    string
				limit    int
				page     int
				fetch    int
				expected []string
			}{
				{name: "defaults", fetch: 10, expected: names[:10]},
				{name: "second page", limit: 2, page: 2, fetch: 4, expected: names[2:4]},
				{name: "filter before limit", regex: "http", limit: 2, fetch: 2, expected: names[1:3]},
				{name: "filtered second page", regex: "http", limit: 2, page: 2, fetch: 4, expected: names[3:4]},
				{name: "anchored", regex: "^b_", fetch: 10, expected: names[1:2]},
				{name: "alternation", regex: "cpu|errors", fetch: 10, expected: []string{names[0], names[2]}},
				{name: "matches empty string", regex: ".*", fetch: 10, expected: names[:10]},
				{name: "no matches", regex: "missing", fetch: 10, expected: []string{}},
				{name: "beyond results", limit: 2, page: 7, fetch: 14, expected: []string{}},
				{name: "maximum fetch", limit: 10000, fetch: 10000, expected: names},
			} {
				t.Run(tc.name, func(t *testing.T) {
					requests := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						switch r.URL.Path {
						case "/api/datasources/uid/test":
							_ = json.NewEncoder(w).Encode(&models.DataSource{UID: "test", Type: datasourceType, JSONData: map[string]interface{}{"defaultProject": "project"}})
						case "/api/datasources/uid/test/resources/metricDescriptors/v3/projects/project/metricDescriptors":
							requests++
							descriptors := make([]gcpMetricDescriptor, len(names))
							for i, name := range names {
								descriptors[i].Type = name
							}
							_ = json.NewEncoder(w).Encode(descriptors)
						case "/api/datasources/uid/test/resources/api/v1/label/__name__/values":
							requests++
							require.NoError(t, r.ParseForm())
							assert.Equal(t, strconv.Itoa(tc.fetch), r.Form.Get("limit"))
							assert.Equal(t, "1700000000", r.Form.Get("start"))
							assert.Equal(t, "1700003600", r.Form.Get("end"))
							matches := names
							if tc.regex == "" {
								assert.Empty(t, r.Form["match[]"])
							} else {
								require.Len(t, r.Form["match[]"], 1)
								matchers, err := parser.NewParser(parser.Options{}).ParseMetricSelector(r.Form.Get("match[]"))
								require.NoError(t, err)
								matches = []string{}
								for _, name := range names {
									matched := true
									for _, matcher := range matchers {
										assert.Equal(t, "__name__", matcher.Name)
										matched = matched && matcher.Matches(name)
									}
									if matched {
										matches = append(matches, name)
									}
								}
							}
							matches = matches[:min(tc.fetch, len(matches))]
							_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": matches})
						default:
							t.Errorf("unexpected request: %s", r.URL)
							http.NotFound(w, r)
						}
					}))
					defer server.Close()
					result, err := listPrometheusMetricNames(mockDatasourcesCtx(server), ListPrometheusMetricNamesParams{
						DatasourceUID: "test", Regex: tc.regex, Limit: tc.limit, Page: tc.page,
						StartRFC3339: "2023-11-14T22:13:20Z", EndRFC3339: "2023-11-14T23:13:20Z",
					})
					require.NoError(t, err)
					assert.Equal(t, tc.expected, result)
					assert.Equal(t, 1, requests)
				})
			}
		})
	}
}

func TestListPrometheusMetricNamesRejectsInvalidRequests(t *testing.T) {
	for _, args := range []ListPrometheusMetricNamesParams{
		{Limit: -1}, {Page: -1}, {Limit: 10001}, {Limit: 10, Page: 1001},
		{Limit: math.MaxInt, Page: math.MaxInt}, {Regex: "["},
	} {
		// No datasource context: validation must happen before making any requests.
		_, err := listPrometheusMetricNames(context.Background(), args)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "getting backend")
	}
}

func TestPrometheusMetricNamesRegexSemantics(t *testing.T) {
	names := []string{"http_requests", "prefix_http_suffix", "HTTP", "a.b", "a\\b", "a\"b", "a\nb", "\nhttp\n", "cpu", "memory"}
	for _, pattern := range []string{"http", "^http", "http$", "^http$", "cpu|memory", `a\.b`, `a\\b`, `a"b`, "(?i)http", "a.b", "(?s)a.b", "(?m)^http$", ".*", "^$", "\\bhttp\\b"} {
		t.Run(pattern, func(t *testing.T) {
			re := regexp.MustCompile(pattern)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, r.ParseForm())
				matchers, err := parser.NewParser(parser.Options{}).ParseMetricSelector(r.Form.Get("match[]"))
				require.NoError(t, err)
				for _, name := range names {
					matched := true
					for _, matcher := range matchers {
						matched = matched && matcher.Matches(name)
					}
					assert.Equal(t, re.MatchString(name), matched, "metric name %q", name)
				}
				_, _ = w.Write([]byte(`{"status":"success","data":[]}`))
			}))
			defer server.Close()
			_, err := newTestPrometheusBackend(t, server).MetricNames(context.Background(), re, 10, time.Time{}, time.Time{})
			require.NoError(t, err)
		})
	}
}
