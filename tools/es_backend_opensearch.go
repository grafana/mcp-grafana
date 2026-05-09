package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
)

// openSearchBackend handles queries to an OpenSearch datasource via the
// Grafana /api/ds/query endpoint, which routes through the OpenSearch
// plugin backend. This ensures proper authentication (including AWS SigV4).
type openSearchBackend struct {
	httpClient      *http.Client
	baseURL         string
	datasourceUID   string
	configuredIndex string
}

func newOpenSearchBackend(ctx context.Context, ds *models.DataSource) (*openSearchBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// The OpenSearch plugin stores the index pattern in jsonData.database,
	// not in the top-level database field.
	configuredIndex := ""
	if jsonData, ok := ds.JSONData.(map[string]interface{}); ok {
		if db, ok := jsonData["database"].(string); ok {
			configuredIndex = db
		}
	}

	return &openSearchBackend{
		httpClient:      client,
		baseURL:         baseURL,
		datasourceUID:   ds.UID,
		configuredIndex: configuredIndex,
	}, nil
}

// indexMatchesPattern reports whether the given index is compatible with the
// datasource's configured index pattern. It handles simple wildcard patterns
// (e.g., "logs-*") using path.Match semantics.
func indexMatchesPattern(pattern, index string) bool {
	if pattern == "" || pattern == index {
		return true
	}
	matched, err := path.Match(pattern, index)
	if err == nil && matched {
		return true
	}
	// If the user's index is itself a pattern (contains wildcards), check
	// whether the non-wildcard prefix matches the configured pattern's prefix.
	// e.g., configured="logs-*", user="logs-2024*" → compatible.
	patternPrefix := strings.TrimRight(pattern, "*?")
	indexPrefix := strings.TrimRight(index, "*?")
	return strings.HasPrefix(indexPrefix, patternPrefix)
}

// Search executes a search query against an OpenSearch datasource using
// the Grafana /api/ds/query endpoint with the OpenSearch plugin's query model.
func (b *openSearchBackend) Search(ctx context.Context, index, query string, startTime, endTime *time.Time, limit int) ([]ElasticsearchDocument, error) {
	// Validate the requested index against the datasource's configured index pattern.
	// The OpenSearch plugin always searches within the configured index, so requests
	// for incompatible indices would silently return no results.
	if b.configuredIndex != "" && !indexMatchesPattern(b.configuredIndex, index) {
		return nil, fmt.Errorf("the requested index %q is not compatible with this datasource's configured index pattern %q; use an index that matches the pattern or choose a different datasource", index, b.configuredIndex)
	}

	// Determine time range in milliseconds
	var fromMs, toMs string
	if startTime != nil {
		fromMs = strconv.FormatInt(startTime.UnixMilli(), 10)
	} else {
		// Default to 10 years ago
		fromMs = strconv.FormatInt(time.Now().Add(-10*365*24*time.Hour).UnixMilli(), 10)
	}
	if endTime != nil {
		toMs = strconv.FormatInt(endTime.UnixMilli(), 10)
	} else {
		toMs = strconv.FormatInt(time.Now().UnixMilli(), 10)
	}

	// Use the user's query as-is. The OpenSearch plugin searches within the
	// datasource's configured index pattern, which we've already validated above.
	luceneQuery := query
	if luceneQuery == "" {
		luceneQuery = "*"
	}

	// Build the /api/ds/query payload using the OpenSearch plugin's query model
	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"refId": "A",
				"datasource": map[string]interface{}{
					"uid":  b.datasourceUID,
					"type": openSearchDatasourceType,
				},
				"query":           luceneQuery,
				"queryType":       "lucene",
				"luceneQueryType": "RawDocument",
				"timeField":       "@timestamp",
				"metrics": []map[string]interface{}{
					{
						"id":   "1",
						"type": "raw_document",
						"settings": map[string]interface{}{
							"size": strconv.Itoa(limit),
						},
					},
				},
				"bucketAggs": []interface{}{},
				"format":     "table",
			},
		},
		"from": fromMs,
		"to":   toMs,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+"/api/ds/query", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 48*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResult map[string]interface{}
		if json.Unmarshal(bodyBytes, &errResult) == nil {
			if errMsg, ok := errResult["message"].(string); ok {
				return nil, fmt.Errorf("opensearch query failed: %s", errMsg)
			}
		}
		return nil, fmt.Errorf("opensearch query failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse the /api/ds/query response
	var result dsQueryResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}

	queryResult, ok := result.Results["A"]
	if !ok {
		return nil, fmt.Errorf("no result found for refId A")
	}
	if queryResult.Error != "" {
		return nil, fmt.Errorf("opensearch query error: %s", queryResult.Error)
	}

	return framesToDocuments(queryResult.Frames)
}

// framesToDocuments converts the OpenSearch plugin's raw_document
// frame response to ElasticsearchDocument objects.
//
// The OpenSearch plugin returns a single frame with one column (named after the refId)
// of type json.RawMessage. Each value in that column is a complete document object
// containing _id, _index, _type, @timestamp (as array), and all source fields.
func framesToDocuments(frames []dsQueryFrame) ([]ElasticsearchDocument, error) {
	if len(frames) == 0 {
		return []ElasticsearchDocument{}, nil
	}

	frame := frames[0]
	if len(frame.Data.Values) == 0 || len(frame.Data.Values[0]) == 0 {
		return []ElasticsearchDocument{}, nil
	}

	rawDocs := frame.Data.Values[0]
	documents := make([]ElasticsearchDocument, 0, len(rawDocs))

	for _, rawDoc := range rawDocs {
		docMap, ok := rawDoc.(map[string]interface{})
		if !ok {
			continue
		}

		doc := ElasticsearchDocument{
			Source: make(map[string]interface{}),
		}

		for key, val := range docMap {
			switch key {
			case "_index":
				if s, ok := val.(string); ok {
					doc.Index = s
				}
			case "_id":
				if s, ok := val.(string); ok {
					doc.ID = s
				}
			case "_type":
				// Skip metadata field
			case "_score":
				if f, ok := val.(float64); ok {
					doc.Score = &f
				}
			case "@timestamp":
				// The plugin returns @timestamp as an array like ["2024-01-01T00:00:00Z"]
				if arr, ok := val.([]interface{}); ok && len(arr) > 0 {
					if ts, ok := arr[0].(string); ok {
						doc.Timestamp = ts
						doc.Source[key] = ts
					}
				} else if ts, ok := val.(string); ok {
					doc.Timestamp = ts
					doc.Source[key] = ts
				}
			default:
				// Skip unknown _-prefixed metadata fields
				if strings.HasPrefix(key, "_") {
					continue
				}
				doc.Source[key] = val
			}
		}

		documents = append(documents, doc)
	}

	return documents, nil
}
