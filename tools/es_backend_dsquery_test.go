package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// esStub serves an Elasticsearch plugin of the given version. /api/ds/query
// answers with a raw_document frame and the proxy with an _msearch response;
// the last ds/query payload and the routes hit are recorded.
func esStub(t *testing.T, version string) (*httptest.Server, *map[string]interface{}, *[]string) {
	payload := &map[string]interface{}{}
	routes := &[]string{}
	doc := json.RawMessage(`{"_id":"1","_index":"logs-1","_type":null,"sort":[1718452800000,1],"highlight":null,"@timestamp":1718452800000,"message":"hello"}`)
	frames, err := json.Marshal(&backend.QueryDataResponse{Responses: backend.Responses{
		"A": {Frames: data.Frames{data.NewFrame("A", data.NewField("A", nil, []*json.RawMessage{&doc}))}},
	}})
	require.NoError(t, err)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/plugins/elasticsearch/settings":
			if version == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"info":{"version":"`+version+`"}}`)
		case "/api/ds/query":
			*routes = append(*routes, "ds/query")
			_ = json.NewDecoder(r.Body).Decode(payload)
			_, _ = w.Write(frames)
		case "/api/datasources/proxy/uid/es/_msearch":
			*routes = append(*routes, "proxy")
			_, _ = io.WriteString(w, `{"responses":[{"hits":{"hits":[{"_id":"1","_index":"logs-1","_source":{"@timestamp":1718452800000,"message":"hello"}}]}}]}`)
		default:
			http.NotFound(w, r)
		}
	})), payload, routes
}

func TestElasticsearchBackendRoutesByPluginVersion(t *testing.T) {
	const dsl = `{"term":{"level":"error"}}`
	for _, tc := range []struct {
		version     string
		lucene, dsl string
	}{
		{"12.9.3", "ds/query", "ds/query"},
		{"13.0.0", "ds/query", "ds/query"},
		{"12.6.2", "ds/query", "proxy"},
		{"12.4.0", "proxy", "proxy"},
		{"", "proxy", "proxy"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			srv, _, routes := esStub(t, tc.version)
			defer srv.Close()
			ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: srv.URL})
			b, err := newElasticsearchBackend(ctx, &models.DataSource{UID: "es", Type: elasticsearchDatasourceType})
			require.NoError(t, err)

			for _, q := range []string{"level:error", dsl} {
				docs, err := b.Search(ctx, "logs-*", q, time.Time{}, time.Time{}, 5)
				require.NoError(t, err)
				require.Len(t, docs, 1)
				assert.Equal(t, "2024-06-15T12:00:00Z", docs[0].Timestamp)
				assert.Equal(t, map[string]interface{}{"@timestamp": float64(1718452800000), "message": "hello"}, docs[0].Source)
			}
			assert.Equal(t, []string{tc.lucene, tc.dsl}, *routes)
		})
	}
}

func TestElasticsearchBackendDSQueryPayload(t *testing.T) {
	srv, payload, _ := esStub(t, "12.9.3")
	defer srv.Close()
	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: srv.URL})
	b, err := newElasticsearchBackend(ctx, &models.DataSource{UID: "es", Type: elasticsearchDatasourceType})
	require.NoError(t, err)
	start := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	query := func() map[string]interface{} {
		assert.Equal(t, "1718409600000", (*payload)["from"])
		assert.Equal(t, "1718413200000", (*payload)["to"])
		q := (*payload)["queries"].([]interface{})[0].(map[string]interface{})
		assert.Equal(t, "logs-*", q["index"])
		assert.Equal(t, "5", q["metrics"].([]interface{})[0].(map[string]interface{})["settings"].(map[string]interface{})["size"])
		return q
	}

	_, err = b.Search(ctx, "logs-*", "level:error", start, end, 5)
	require.NoError(t, err)
	q := query()
	assert.Equal(t, "level:error", q["query"])
	assert.Nil(t, q["queryType"])

	_, err = b.Search(ctx, "logs-*", `{"term":{"level":"error"}}`, start, end, 5)
	require.NoError(t, err)
	q = query()
	assert.Equal(t, "dsl", q["queryType"])
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(q["query"].(string)), &body))
	assert.Equal(t, float64(5), body["size"])
	assert.Contains(t, q["query"], `"range":{"@timestamp":{"gte":"2024-06-15T00:00:00Z","lte":"2024-06-15T01:00:00Z"}}`)
	assert.Contains(t, q["query"], `{"term":{"level":"error"}}`)
}
