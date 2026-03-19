package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultOpenSearchLimit is the default number of documents to return if not specified
	DefaultOpenSearchLimit = 10

	// MaxOpenSearchLimit is the maximum number of documents that can be requested
	MaxOpenSearchLimit = 100

	// OpenSearchDatasourceType is the type identifier for OpenSearch datasources
	OpenSearchDatasourceType = "grafana-opensearch-datasource"
)

// OpenSearchDocument represents a single document from search results
type OpenSearchDocument struct {
	Index     string                 `json:"_index"`
	ID        string                 `json:"_id"`
	Score     *float64               `json:"_score,omitempty"`
	Source    map[string]interface{} `json:"_source"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
}

// QueryOpenSearchParams defines the parameters for querying OpenSearch
type QueryOpenSearchParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the OpenSearch datasource to query"`
	Index         string `json:"index" jsonschema:"required,description=The index pattern to search (e.g.\\, 'logs-*'\\, 'filebeat-*'\\, or a specific index name)"`
	Query         string `json:"query" jsonschema:"required,description=The search query using Lucene query syntax (e.g.\\, 'status:200 AND host:server1'\\, 'level:error'\\, or '*' for all documents)"`
	StartTime     string `json:"startTime,omitempty" jsonschema:"description=Optionally\\, the start time in RFC3339 format (e.g.\\, '2024-01-01T00:00:00Z'). Filters results to documents with @timestamp >= this value"`
	EndTime       string `json:"endTime,omitempty" jsonschema:"description=Optionally\\, the end time in RFC3339 format (e.g.\\, '2024-01-01T23:59:59Z'). Filters results to documents with @timestamp <= this value"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=10,description=Optionally\\, the maximum number of documents to return (max: 100\\, default: 10)"`
}

// queryOpenSearch executes a search query against an OpenSearch datasource
// using Grafana's /api/ds/query endpoint, which routes through the OpenSearch
// plugin backend. This ensures proper authentication (including AWS SigV4).
func queryOpenSearch(ctx context.Context, args QueryOpenSearchParams) ([]OpenSearchDocument, error) {
	// Validate datasource exists and is the correct type
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("creating OpenSearch client: %w", err)
	}
	if ds.Type != OpenSearchDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", args.DatasourceUID, ds.Type, OpenSearchDatasourceType)
	}

	// Apply limit constraints
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultOpenSearchLimit
	}
	if limit > MaxOpenSearchLimit {
		limit = MaxOpenSearchLimit
	}

	// Determine time range
	var fromMs, toMs string
	if args.StartTime != "" {
		t, err := time.Parse(time.RFC3339, args.StartTime)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		fromMs = strconv.FormatInt(t.UnixMilli(), 10)
	} else {
		// Default to 10 years ago
		fromMs = strconv.FormatInt(time.Now().Add(-10*365*24*time.Hour).UnixMilli(), 10)
	}
	if args.EndTime != "" {
		t, err := time.Parse(time.RFC3339, args.EndTime)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		toMs = strconv.FormatInt(t.UnixMilli(), 10)
	} else {
		// Default to now
		toMs = strconv.FormatInt(time.Now().UnixMilli(), 10)
	}

	// Build the Lucene query with index filter.
	// The OpenSearch plugin uses the datasource's configured index pattern by default,
	// so we prepend _index:<pattern> to filter by the user-specified index.
	query := args.Query
	if query == "*" || query == "" {
		query = "_index:" + args.Index
	} else {
		query = "_index:" + args.Index + " AND (" + query + ")"
	}

	// Build the /api/ds/query payload using the OpenSearch plugin's query model
	payload := map[string]interface{}{
		"queries": []map[string]interface{}{
			{
				"refId": "A",
				"datasource": map[string]interface{}{
					"uid":  args.DatasourceUID,
					"type": OpenSearchDatasourceType,
				},
				"query":           query,
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

	// Execute the request
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
		Timeout:   30 * time.Second,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/ds/query", bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
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

	// Convert frames to documents
	return framesToOpenSearchDocuments(queryResult.Frames)
}

// framesToOpenSearchDocuments converts the OpenSearch plugin's raw_document
// frame response to OpenSearchDocument objects.
//
// The OpenSearch plugin returns a single frame with one column (named after the refId)
// of type json.RawMessage. Each value in that column is a complete document object
// containing _id, _index, _type, @timestamp (as array), and all source fields.
func framesToOpenSearchDocuments(frames []dsQueryFrame) ([]OpenSearchDocument, error) {
	if len(frames) == 0 {
		return []OpenSearchDocument{}, nil
	}

	frame := frames[0]
	if len(frame.Data.Values) == 0 || len(frame.Data.Values[0]) == 0 {
		return []OpenSearchDocument{}, nil
	}

	// The first (and only) column contains all documents as JSON objects
	rawDocs := frame.Data.Values[0]
	documents := make([]OpenSearchDocument, 0, len(rawDocs))

	for _, rawDoc := range rawDocs {
		docMap, ok := rawDoc.(map[string]interface{})
		if !ok {
			continue
		}

		doc := OpenSearchDocument{
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
				doc.Source[key] = val
			}
		}

		documents = append(documents, doc)
	}

	return documents, nil
}

// QueryOpenSearch is a tool for querying OpenSearch datasources
var QueryOpenSearch = mcpgrafana.MustTool(
	"query_opensearch",
	"Executes a search query against an OpenSearch datasource and retrieves matching documents. Supports Lucene query syntax (e.g., 'status:200 AND host:server1', 'level:error', or '*' for all documents). Returns a list of documents with their index, ID, source fields, and optional score. Use this to search logs, metrics, or any indexed data stored in OpenSearch. Defaults to 10 results and sorts by @timestamp in descending order (newest first).",
	queryOpenSearch,
	mcp.WithTitleAnnotation("Query OpenSearch"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddOpenSearchTools registers all OpenSearch tools with the MCP server
func AddOpenSearchTools(mcp *server.MCPServer) {
	QueryOpenSearch.Register(mcp)
}
