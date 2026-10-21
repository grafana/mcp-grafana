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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLokiClient_FetchData_PassesMatcherAsQueryParam(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":["service"]}`))
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client(), baseURL: server.URL}

	result, err := c.fetchData(context.Background(), "/loki/api/v1/label/service/values", `{namespace="prod"}`, "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"service"}, result)
	assert.Equal(t, `{namespace="prod"}`, gotQuery)
}

func TestLokiClient_FetchData_OmitsQueryParamWhenMatcherEmpty(t *testing.T) {
	var sawQuery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawQuery = r.URL.Query()["query"]
		_, _ = w.Write([]byte(`{"status":"success","data":["app","pod"]}`))
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client(), baseURL: server.URL}

	result, err := c.fetchData(context.Background(), "/loki/api/v1/labels", "", "", "")
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "pod"}, result)
	assert.False(t, sawQuery, "query param should be omitted when matcher is empty")
}

// lokiQueryStub serves body as the Loki query response through the Grafana
// datasource proxy, and records the query parameters it received.
func lokiQueryStub(body string) (*httptest.Server, *url.Values) {
	query := &url.Values{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case "/api/datasources/proxy/uid/loki/loki/api/v1/query_range",
			"/api/datasources/proxy/uid/loki/loki/api/v1/query":
			*query = r.URL.Query()
			_, _ = io.WriteString(w, body)
		default:
			http.NotFound(w, r)
		}
	})), query
}

func TestQueryLokiLogsSortsBeforeTruncating(t *testing.T) {
	server, _ := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a"},"values":[["1000000002","a2"],["1000000001","a1"]]},
		{"stream":{"app":"b"},"values":[["1000000003","b3"]]}
	]}}`)
	defer server.Close()
	ctx := enforceTestCtx(server, false)

	for _, tc := range []struct {
		direction string
		lines     []string
	}{
		{direction: "backward", lines: []string{"b3", "a2"}},
		{direction: "forward", lines: []string{"a1", "a2"}},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			result, err := queryLokiLogs(ctx, QueryLokiLogsParams{
				DatasourceUID: "loki", LogQL: `{app=~".+"}`, Limit: 2, Direction: tc.direction,
			})
			require.NoError(t, err)
			require.Len(t, result.Data, 2)
			assert.Equal(t, tc.lines, []string{result.Data[0].Line, result.Data[1].Line})
			require.NotNil(t, result.Metadata)
			assert.True(t, result.Metadata.ResultsTruncated)
		})
	}
}

func TestQueryLokiLogsRejectsUnparseableStreamTimestamp(t *testing.T) {
	server, _ := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a"},"values":[["not-a-timestamp","a1"]]}
	]}}`)
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-timestamp")
}

// Loki timestamps are nanosecond-resolution, so a bound one nanosecond off a
// second boundary must survive both parsing and formatting unchanged.
func TestQueryLokiLogsPreservesNanosecondBounds(t *testing.T) {
	server, query := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[]}}`)
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`,
		StartRFC3339: "2024-01-15T10:00:00.000000001Z", EndRFC3339: "2024-01-15T10:00:00.000000004Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "1705312800000000001", query.Get("start"))
	assert.Equal(t, "1705312800000000004", query.Get("end"))

	_, err = queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `count_over_time({app="a"}[5m])`, QueryType: "instant",
		EndRFC3339: "2024-01-15T10:00:00.000000004Z",
	})
	require.NoError(t, err)
	assert.Equal(t, "1705312800000000004", query.Get("time"))
}

// grafanaLokiStub mimics how Grafana routes a Loki datasource: the proxy
// path forwards the request path to Loki unchanged, while the /resources
// handler prepends /loki/api/v1/ itself (pkg/tsdb/loki callResource). Loki
// answers a path it does not serve with its UI's index.html, which Grafana's
// resource handler relays as Content-Type: application/json.
func grafanaLokiStub(proxyStatus int, body string) (*httptest.Server, *[]string) {
	var paths []string
	const proxyBase = "/api/datasources/proxy/uid/loki"
	const resourcesBase = "/api/datasources/uid/loki/resources/"
	serveLoki := func(w http.ResponseWriter, lokiPath string) {
		w.Header().Set("Content-Type", "application/json")
		switch lokiPath {
		case "/loki/api/v1/query_range", "/loki/api/v1/query":
			_, _ = io.WriteString(w, body)
		default:
			_, _ = io.WriteString(w, "<!DOCTYPE html>\n<html lang=\"en\"><head><title>Loki UI</title></head></html>")
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case r.URL.Path == "/api/datasources/uid/loki":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case strings.HasPrefix(r.URL.Path, proxyBase+"/"):
			if proxyStatus != http.StatusOK {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(proxyStatus)
				_, _ = io.WriteString(w, `{"message":"query timed out"}`)
				return
			}
			serveLoki(w, strings.TrimPrefix(r.URL.Path, proxyBase))
		case strings.HasPrefix(r.URL.Path, resourcesBase):
			if r.URL.Query().Get("query") == "" {
				http.Error(w, "query parameter not forwarded", http.StatusBadRequest)
				return
			}
			serveLoki(w, "/loki/api/v1/"+strings.TrimPrefix(r.URL.Path, resourcesBase))
		default:
			http.NotFound(w, r)
		}
	})), &paths
}

// A 500 on the proxy path falls back to /resources. For Loki that route must
// drop the /loki/api/v1 prefix, which Grafana adds back; otherwise Loki gets
// /loki/api/v1/loki/api/v1/query_range and answers with its UI page.
func TestQueryLokiLogsFallsBackToLokiResourcesRoute(t *testing.T) {
	resetFallbackCache()
	t.Cleanup(resetFallbackCache)

	server, paths := grafanaLokiStub(http.StatusInternalServerError, `{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a"},"values":[["1000000001","a1"]]}
	]}}`)
	defer server.Close()

	result, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	assert.Equal(t, "a1", result.Data[0].Line)
	assert.Contains(t, *paths, "/api/datasources/uid/loki/resources/query_range")
	assert.NotContains(t, *paths, "/api/datasources/uid/loki/resources/loki/api/v1/query_range")
}
