package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-openapi-client-go/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogFieldsFromDataSource(t *testing.T) {
	tests := []struct {
		name string
		ds   *models.DataSource
		want esLogFields
	}{
		{"nil datasource", nil, esLogFields{message: "message"}},
		{"empty fields", &models.DataSource{JSONData: map[string]interface{}{"logMessageField": "", "logLevelField": ""}}, esLogFields{message: "message"}},
		{"configured", &models.DataSource{JSONData: map[string]interface{}{"logMessageField": "log", "logLevelField": "level"}}, esLogFields{message: "log", level: "level"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, logFieldsFromDataSource(tt.ds))
		})
	}
}

func TestSelectSourceFields(t *testing.T) {
	source := map[string]interface{}{
		"log":          "boom",
		"service.name": "api",
		"kubernetes": map[string]interface{}{
			"pod_name": "api-0",
			"labels":   map[string]interface{}{"app.kubernetes.io/name": "api"},
		},
		"spans": []interface{}{
			map[string]interface{}{"calls": []interface{}{map[string]interface{}{"ms": 1.0}, map[string]interface{}{"ms": 2.0}}},
			map[string]interface{}{"other": true},
			map[string]interface{}{"calls": []interface{}{map[string]interface{}{"ms": 3.0}}},
		},
	}

	got := selectSourceFields(source, []string{
		"log",
		"service.name",
		"kubernetes.pod_name",
		"kubernetes.labels.app.kubernetes.io/name",
		"spans.calls.ms",
		"kubernetes.missing",
		" ",
	})
	assert.Equal(t, map[string]interface{}{
		"log":                 "boom",
		"service.name":        "api",
		"kubernetes.pod_name": "api-0",
		"kubernetes.labels.app.kubernetes.io/name": "api",
		"spans.calls.ms": []interface{}{1.0, 2.0, 3.0},
	}, got)
}

// esStubGrafana serves a Grafana with one datasource "ds" of the given type
// and jsonData, holding one document with the given source. The document is
// served by /api/ds/query and by the datasource proxy; the Elasticsearch
// plugin version decides which of them the backend calls.
func esStubGrafana(t *testing.T, dsType, pluginVersion, jsonData, source string) context.Context {
	t.Helper()
	doc := json.RawMessage(`{"_id":"1","_index":"logs-1",` + source[1:])
	frames, err := json.Marshal(&backend.QueryDataResponse{Responses: backend.Responses{
		"A": {Frames: data.Frames{data.NewFrame("A", data.NewField("A", nil, []*json.RawMessage{&doc}))}},
	}})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/datasources/uid/ds":
			_, _ = io.WriteString(w, `{"uid":"ds","type":"`+dsType+`","jsonData":`+jsonData+`}`)
		case "/api/plugins/elasticsearch/settings":
			_, _ = io.WriteString(w, `{"info":{"version":"`+pluginVersion+`"}}`)
		case "/api/ds/query":
			_, _ = w.Write(frames)
		case "/api/datasources/proxy/uid/ds/_msearch":
			_, _ = io.WriteString(w, `{"responses":[{"hits":{"hits":[{"_id":"1","_index":"logs-1","_source":`+source+`}]}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx := mcpgrafana.WithGrafanaConfig(context.Background(), mcpgrafana.GrafanaConfig{URL: srv.URL, APIKey: "test"})
	return mcpgrafana.WithGrafanaClient(ctx, mcpgrafana.NewGrafanaClient(ctx, srv.URL, "test", nil))
}

func TestQueryElasticsearchFormat(t *testing.T) {
	ctx := esStubGrafana(t, elasticsearchDatasourceType, "12.9.3",
		`{"timeField":"@timestamp","logMessageField":"log","logLevelField":"level"}`,
		`{"@timestamp":"2024-06-15T12:00:00Z","log":"boom","level":"error","kubernetes":{"pod_name":"api-0"}}`)
	query := func(format string, fields ...string) ElasticsearchDocument {
		t.Helper()
		docs, err := queryElasticsearch(ctx, QueryElasticsearchParams{DatasourceUID: "ds", Index: "logs-*", Query: "*", Format: format, Fields: fields})
		require.NoError(t, err)
		require.Len(t, docs, 1)
		return docs[0]
	}

	t.Run("full returns the whole document", func(t *testing.T) {
		doc := query("")
		assert.Equal(t, "1", doc.ID)
		assert.Len(t, doc.Source, 4)
	})

	t.Run("full with fields keeps only those source fields", func(t *testing.T) {
		doc := query("full", "kubernetes.pod_name", "log")
		assert.Equal(t, "1", doc.ID)
		assert.Equal(t, map[string]interface{}{"kubernetes.pod_name": "api-0", "log": "boom"}, doc.Source)
	})

	t.Run("compact keeps the timestamp, message and level", func(t *testing.T) {
		out, err := json.Marshal(query("compact"))
		require.NoError(t, err)
		assert.JSONEq(t, `{"timestamp":"2024-06-15T12:00:00Z","_source":{"log":"boom","level":"error"}}`, string(out))
	})

	t.Run("compact with fields adds them", func(t *testing.T) {
		doc := query("COMPACT", "kubernetes.pod_name")
		assert.Equal(t, map[string]interface{}{"log": "boom", "level": "error", "kubernetes.pod_name": "api-0"}, doc.Source)
	})

	t.Run("invalid format is rejected", func(t *testing.T) {
		_, err := queryElasticsearch(ctx, QueryElasticsearchParams{DatasourceUID: "ds", Index: "logs-*", Query: "*", Format: "short"})
		assert.ErrorContains(t, err, `invalid format "short"`)
	})
}

func TestQueryElasticsearchCompactTimestamp(t *testing.T) {
	// The time field can be a nested path, hold an array of values and hold
	// epoch milliseconds; compact keeps the time only in timestamp.
	sources := []struct {
		name, timeField, source string
	}{
		{"nested array", "event.created", `{"event":{"created":["2024-06-15T12:00:00Z"]}}`},
		{"array of epoch milliseconds", "@timestamp", `{"@timestamp":[1718452800000]}`},
	}
	paths := []struct {
		name, dsType, pluginVersion string
	}{
		{"elasticsearch via ds/query", elasticsearchDatasourceType, "12.9.3"},
		{"elasticsearch via proxy", elasticsearchDatasourceType, "12.5.0"},
		{"opensearch", openSearchDatasourceType, ""},
	}
	for _, src := range sources {
		for _, p := range paths {
			t.Run(src.name+"/"+p.name, func(t *testing.T) {
				ctx := esStubGrafana(t, p.dsType, p.pluginVersion, `{"timeField":"`+src.timeField+`","database":"logs-*"}`, src.source)
				docs, err := queryElasticsearch(ctx, QueryElasticsearchParams{DatasourceUID: "ds", Index: "logs-*", Query: "*", Format: "compact"})
				require.NoError(t, err)
				require.Len(t, docs, 1)
				assert.Equal(t, "2024-06-15T12:00:00Z", docs[0].Timestamp)
			})
		}
	}
}
