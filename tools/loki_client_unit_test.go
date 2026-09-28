//go:build unit

package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

// lokiQueryStub serves body as the Loki range-query response through the
// Grafana datasource proxy.
func lokiQueryStub(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/loki":
			_, _ = io.WriteString(w, `{"uid":"loki","type":"loki"}`)
		case "/api/datasources/proxy/uid/loki/loki/api/v1/query_range":
			_, _ = io.WriteString(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestQueryLokiLogsSortsBeforeTruncating(t *testing.T) {
	server := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[
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
	server := lokiQueryStub(`{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"a"},"values":[["not-a-timestamp","a1"]]}
	]}}`)
	defer server.Close()

	_, err := queryLokiLogs(enforceTestCtx(server, false), QueryLokiLogsParams{
		DatasourceUID: "loki", LogQL: `{app="a"}`, Limit: 10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-timestamp")
}
