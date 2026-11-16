package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimestampInfoFromMetadata(t *testing.T) {
	t.Run("parses timestamp field from metadata", func(t *testing.T) {
		metadata := []quickwitIndexMetadata{
			{
				IndexConfig: struct {
					IndexID    string `json:"index_id"`
					DocMapping struct {
						TimestampField string                 `json:"timestamp_field"`
						FieldMappings  []quickwitFieldMapping `json:"field_mappings"`
					} `json:"doc_mapping"`
				}{
					IndexID: "test-logs",
					DocMapping: struct {
						TimestampField string                 `json:"timestamp_field"`
						FieldMappings  []quickwitFieldMapping `json:"field_mappings"`
					}{
						TimestampField: "timestamp",
						FieldMappings: []quickwitFieldMapping{
							{Name: "timestamp", Type: "datetime", OutputFormat: quickwitStrPtr("rfc3339")},
							{Name: "body", Type: "text"},
						},
					},
				},
			},
		}

		timeField, outputFormat, err := timestampInfoFromMetadata(metadata)
		require.NoError(t, err)
		assert.Equal(t, "timestamp", timeField)
		assert.Equal(t, "rfc3339", outputFormat)
	})

	t.Run("detects nanosecond output format", func(t *testing.T) {
		metadata := []quickwitIndexMetadata{
			{
				IndexConfig: struct {
					IndexID    string `json:"index_id"`
					DocMapping struct {
						TimestampField string                 `json:"timestamp_field"`
						FieldMappings  []quickwitFieldMapping `json:"field_mappings"`
					} `json:"doc_mapping"`
				}{
					IndexID: "otel-logs",
					DocMapping: struct {
						TimestampField string                 `json:"timestamp_field"`
						FieldMappings  []quickwitFieldMapping `json:"field_mappings"`
					}{
						TimestampField: "timestamp",
						FieldMappings: []quickwitFieldMapping{
							{Name: "timestamp", Type: "datetime", OutputFormat: quickwitStrPtr("unix_timestamp_nanos")},
						},
					},
				},
			},
		}

		_, outputFormat, err := timestampInfoFromMetadata(metadata)
		require.NoError(t, err)
		assert.Equal(t, "unix_timestamp_nanos", outputFormat)
		assert.Equal(t, "epoch_nanos_int", quickwitTimestampSortFormat(outputFormat))
	})

	t.Run("empty metadata returns error", func(t *testing.T) {
		_, _, err := timestampInfoFromMetadata(nil)
		require.Error(t, err)
	})
}

const quickwitTestMetadata = `[{
	"index_config": {
		"index_id": "test-logs",
		"doc_mapping": {
			"timestamp_field": "timestamp",
			"field_mappings": [
				{"name": "timestamp", "type": "datetime", "output_format": "rfc3339"},
				{"name": "body", "type": "text"},
				{"name": "severity_text", "type": "text"}
			]
		}
	}
}]`

// newQuickwitTestServer serves index metadata under /proxy, plus whichever of
// /api/ds/query and /proxy/_elastic/_msearch the test handles.
func newQuickwitTestServer(t *testing.T, dsQuery, msearch http.HandlerFunc) *quickwitBackend {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/indexes":
			assert.Equal(t, "test-logs", r.URL.Query().Get("index_id_patterns"))
			_, _ = io.WriteString(w, quickwitTestMetadata)
		case r.Method == http.MethodPost && r.URL.Path == "/api/ds/query" && dsQuery != nil:
			dsQuery(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/proxy/_elastic/_msearch" && msearch != nil:
			msearch(w, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &quickwitBackend{
		httpClient:      server.Client(),
		baseURL:         server.URL + "/proxy",
		dsQueryClient:   server.Client(),
		grafanaURL:      server.URL,
		dsUID:           "qw",
		configuredIndex: "test-logs",
	}
}

func TestQuickwitBackendSearchDSQuery(t *testing.T) {
	// Response shape captured from quickwit-quickwit-datasource 0.6.0.
	dsQueryResponse := `{"results":{"A":{"status":200,"frames":[{"schema":{"name":"A","refId":"A","fields":[{"name":"A","type":"other","typeInfo":{"frame":"json.RawMessage","nullable":true}}]},"data":{"values":[[{"body":"hello quickwit","severity_text":"ERROR","timestamp":"2024-06-15T12:00:00Z"}]]}}]}}}`

	start := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	backend := newQuickwitTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			From    string                   `json:"from"`
			To      string                   `json:"to"`
			Queries []map[string]interface{} `json:"queries"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, strconv.FormatInt(start.UnixMilli(), 10), payload.From)
		assert.Equal(t, strconv.FormatInt(end.UnixMilli(), 10), payload.To)
		require.Len(t, payload.Queries, 1)
		q := payload.Queries[0]
		assert.Equal(t, "severity_text:ERROR", q["query"])
		assert.Equal(t, map[string]interface{}{"uid": "qw", "type": quickwitDatasourceType}, q["datasource"])
		assert.Equal(t, []interface{}{map[string]interface{}{
			"id": "1", "type": "raw_document", "settings": map[string]interface{}{"size": "10"},
		}}, q["metrics"])
		_, _ = io.WriteString(w, dsQueryResponse)
	}, nil)

	docs, err := backend.Search(context.Background(), "", "severity_text:ERROR", start, end, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "test-logs", docs[0].Index)
	assert.Equal(t, "2024-06-15T12:00:00Z", docs[0].Timestamp)
	assert.Equal(t, "hello quickwit", docs[0].Source["body"])
}

func TestQuickwitBackendSearchDSQueryError(t *testing.T) {
	backend := newQuickwitTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"results":{"A":{"status":500,"error":"failed to parse query","frames":[]}}}`)
	}, nil)

	_, err := backend.Search(context.Background(), "", "bad:(", time.Time{}, time.Time{}, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse query")
}

func TestQuickwitBackendSearchProxy(t *testing.T) {
	msearchResponseBody := `{
		"responses": [{
			"status": 200,
			"hits": {
				"hits": [{
					"_index": "test-logs",
					"_source": {
						"timestamp": "2024-06-15T12:00:00Z",
						"body": "hello quickwit",
						"service_name": "api"
					}
				}]
			}
		}]
	}`
	msearch := func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		require.Len(t, lines, 2)

		var query map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(lines[1]), &query))
		sort, ok := query["sort"].([]interface{})
		require.True(t, ok)
		require.Len(t, sort, 1)
		sortEntry, ok := sort[0].(map[string]interface{})
		require.True(t, ok)
		tsSort, ok := sortEntry["timestamp"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "desc", tsSort["order"])

		_, _ = io.WriteString(w, msearchResponseBody)
	}

	check := func(t *testing.T, docs []ElasticsearchDocument, err error) {
		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "test-logs", docs[0].Index)
		assert.Equal(t, "2024-06-15T12:00:00Z", docs[0].Timestamp)
	}

	t.Run("query DSL JSON", func(t *testing.T) {
		backend := newQuickwitTestServer(t, nil, msearch)
		docs, err := backend.Search(context.Background(), "", `{"term":{"service_name":"api"}}`, time.Time{}, time.Time{}, 10)
		check(t, docs, err)
	})

	t.Run("narrower index than configured pattern", func(t *testing.T) {
		backend := newQuickwitTestServer(t, nil, msearch)
		backend.configuredIndex = "test-*"
		docs, err := backend.Search(context.Background(), "test-logs", "service_name:api", time.Time{}, time.Time{}, 10)
		check(t, docs, err)
	})
}

func TestQuickwitFramesToDocuments(t *testing.T) {
	frame := data.NewFrame("A", data.NewField("A", nil, []json.RawMessage{
		json.RawMessage(`{"ts":"2024-01-01T00:00:00Z","msg":"a","nested":{"k":1}}`),
		json.RawMessage(`{"ts":1704067200000,"msg":"b"}`),
		json.RawMessage(`{"msg":"c"}`),
	}))

	docs, err := quickwitFramesToDocuments(data.Frames{frame}, "idx", "ts")
	require.NoError(t, err)
	require.Len(t, docs, 3)
	assert.Equal(t, "idx", docs[0].Index)
	assert.Equal(t, "2024-01-01T00:00:00Z", docs[0].Timestamp)
	assert.Equal(t, map[string]interface{}{"k": float64(1)}, docs[0].Source["nested"])
	assert.Equal(t, "2024-01-01T00:00:00Z", docs[1].Timestamp)
	assert.Equal(t, "", docs[2].Timestamp)
	assert.Equal(t, "c", docs[2].Source["msg"])

	docs, err = quickwitFramesToDocuments(nil, "idx", "ts")
	require.NoError(t, err)
	assert.Empty(t, docs)
}

func TestQuickwitIndexMatchesPattern(t *testing.T) {
	tests := []struct {
		pattern string
		index   string
		want    bool
	}{
		{pattern: "test-logs", index: "test-logs", want: true},
		{pattern: "test-logs", index: "other-logs", want: false},
		{pattern: "logs-*", index: "logs-2024", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.index, func(t *testing.T) {
			assert.Equal(t, tt.want, indexMatchesPattern(tt.pattern, tt.index))
		})
	}
}

func quickwitStrPtr(s string) *string {
	return &s
}

func TestExecuteMSearch(t *testing.T) {
	responseBody := `{
		"responses": [{
			"hits": {
				"hits": [{
					"_index": "idx",
					"_id": "1",
					"_source": {"@timestamp": "2024-01-02T00:00:00Z", "msg": "hi"}
				}]
			}
		}]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/_msearch", r.URL.Path)
		assert.Equal(t, "application/x-ndjson", r.Header.Get("Content-Type"))
		_, _ = io.WriteString(w, responseBody)
	}))
	t.Cleanup(server.Close)

	docs, err := executeMSearch(
		context.Background(),
		server.Client(),
		buildURL(server.URL, "/_msearch"),
		"idx",
		esSearchQuery{query: "*", size: 1, timeField: defaultTimeField}.build(),
		defaultTimeField,
	)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "2024-01-02T00:00:00Z", docs[0].Timestamp)
}

func TestQuickwitBackendIndexValidation(t *testing.T) {
	backend := &quickwitBackend{
		configuredIndex: "test-logs",
	}
	_, err := backend.Search(context.Background(), "other-index", "*", time.Time{}, time.Time{}, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not compatible")
}

func TestQuickwitResolveIndexDefaults(t *testing.T) {
	backend := &quickwitBackend{configuredIndex: "test-logs"}
	index, err := backend.resolveIndex("")
	require.NoError(t, err)
	assert.Equal(t, "test-logs", index)
}

func TestQuickwitResolveIndexMissing(t *testing.T) {
	backend := &quickwitBackend{}
	_, err := backend.resolveIndex("")
	require.Error(t, err)
}

func TestQuickwitTimestampFieldNestedMapping(t *testing.T) {
	mappings := []quickwitFieldMapping{
		{
			Name: "resource",
			Type: "object",
			FieldMappings: []quickwitFieldMapping{
				{Name: "timestamp", Type: "datetime", OutputFormat: quickwitStrPtr("rfc3339")},
			},
		},
	}
	outputFormat, found := findQuickwitTimestampFormat("resource.timestamp", nil, mappings)
	assert.True(t, found)
	assert.Equal(t, "rfc3339", outputFormat)
}

func TestQuickwitTimestampFieldDeeplyNestedMapping(t *testing.T) {
	mappings := []quickwitFieldMapping{
		{
			Name: "resource",
			Type: "object",
			FieldMappings: []quickwitFieldMapping{
				{
					Name: "attributes",
					Type: "object",
					FieldMappings: []quickwitFieldMapping{
						{Name: "timestamp", Type: "datetime", OutputFormat: quickwitStrPtr("unix_timestamp_nanos")},
					},
				},
			},
		},
	}
	outputFormat, found := findQuickwitTimestampFormat("resource.attributes.timestamp", nil, mappings)
	assert.True(t, found)
	assert.Equal(t, "unix_timestamp_nanos", outputFormat)

	// A field with the same leaf path but a different parent must not match.
	_, found = findQuickwitTimestampFormat("other.attributes.timestamp", nil, mappings)
	assert.False(t, found)
}
