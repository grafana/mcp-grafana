package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestQuickwitBackendSearch(t *testing.T) {
	msearchResponseBody := `{
		"took": 1,
		"responses": [{
			"took": 1,
			"timed_out": false,
			"status": 200,
			"_shards": {"total": 1, "successful": 1, "failed": 0},
			"hits": {
				"total": {"value": 1, "relation": "eq"},
				"max_score": 1.0,
				"hits": [{
					"_index": "test-logs",
					"_id": "1",
					"_score": 1.0,
					"_source": {
						"timestamp": "2024-06-15T12:00:00Z",
						"body": "hello quickwit",
						"severity_text": "ERROR"
					}
				}]
			}
		}]
	}`

	metadataResponseBody := `[{
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/indexes"):
			assert.Equal(t, "test-logs", r.URL.Query().Get("index_id_patterns"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, metadataResponseBody)
		case r.Method == http.MethodPost && r.URL.Path == "/_elastic/_msearch":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			require.Len(t, lines, 2)

			var header map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(lines[0]), &header))
			assert.Equal(t, "test-logs", header["index"])

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

			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, msearchResponseBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	backend := &quickwitBackend{
		httpClient:      server.Client(),
		baseURL:         server.URL,
		configuredIndex: "test-logs",
	}

	docs, err := backend.Search(context.Background(), "test-logs", "severity_text:ERROR", time.Time{}, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "test-logs", docs[0].Index)
	assert.Equal(t, "1", docs[0].ID)
	assert.Equal(t, "2024-06-15T12:00:00Z", docs[0].Timestamp)
	assert.Equal(t, "hello quickwit", docs[0].Source["body"])
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
