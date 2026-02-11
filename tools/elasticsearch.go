package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultElasticsearchLimit is the default number of documents to return if not specified
	DefaultElasticsearchLimit = 10

	// MaxElasticsearchLimit is the maximum number of documents that can be requested
	MaxElasticsearchLimit = 100
)

// ElasticsearchClient handles queries to an Elasticsearch datasource via Grafana proxy
type ElasticsearchClient struct {
	httpClient *http.Client
	baseURL    string
}

// ElasticsearchResponse represents a generic Elasticsearch search response
type ElasticsearchResponse struct {
	Took     int                    `json:"took"`
	TimedOut bool                   `json:"timed_out"`
	Status   int                    `json:"status"`
	Error    interface{}            `json:"error,omitempty"`
	Shards   map[string]interface{} `json:"_shards"`
	Hits     struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		MaxScore *float64                 `json:"max_score"`
		Hits     []map[string]interface{} `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]interface{} `json:"aggregations,omitempty"`
}

// ElasticsearchDocument represents a single document from search results
type ElasticsearchDocument struct {
	Index     string                 `json:"_index"`
	ID        string                 `json:"_id"`
	Score     *float64               `json:"_score,omitempty"`
	Source    map[string]interface{} `json:"_source"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
}

func newElasticsearchClient(ctx context.Context, uid string) (*ElasticsearchClient, error) {
	// First check if the datasource exists
	_, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	url := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(cfg.URL, "/"), uid)

	// Create custom transport with TLS configuration if available
	var transport = http.DefaultTransport
	if tlsConfig := cfg.TLSConfig; tlsConfig != nil {
		var err error
		transport, err = tlsConfig.HTTPTransport(transport.(*http.Transport))
		if err != nil {
			return nil, fmt.Errorf("failed to create custom transport: %w", err)
		}
	}

	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(
			transport,
		),
	}

	return &ElasticsearchClient{
		httpClient: client,
		baseURL:    url,
	}, nil
}

// buildURL constructs a full URL for an Elasticsearch API endpoint
func (c *ElasticsearchClient) buildURL(urlPath string) string {
	fullURL := c.baseURL
	if !strings.HasSuffix(fullURL, "/") && !strings.HasPrefix(urlPath, "/") {
		fullURL += "/"
	} else if strings.HasSuffix(fullURL, "/") && strings.HasPrefix(urlPath, "/") {
		// Remove the leading slash from urlPath to avoid double slash
		urlPath = strings.TrimPrefix(urlPath, "/")
	}
	return fullURL + urlPath
}

// MsearchResponse represents the response from Elasticsearch _msearch API
type MsearchResponse struct {
	Took      int                     `json:"took"`
	Responses []ElasticsearchResponse `json:"responses"`
}

// search performs a search query against Elasticsearch using the _msearch API.
// Grafana's datasource proxy only allows POST requests to /_msearch for Elasticsearch.
func (c *ElasticsearchClient) search(ctx context.Context, index, query string, startTime, endTime *time.Time, size int) (*ElasticsearchResponse, error) {
	// Build the search query
	searchQuery := buildElasticsearchQuery(query, startTime, endTime, size)

	// Build NDJSON payload for _msearch API
	// Format: header line (index info) + newline + body line (query) + newline
	header := map[string]interface{}{
		"index":              index,
		"ignore_unavailable": true,
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshalling header: %w", err)
	}

	queryBytes, err := json.Marshal(searchQuery)
	if err != nil {
		return nil, fmt.Errorf("marshalling query: %w", err)
	}

	// NDJSON format: each JSON object on its own line, ending with newline
	var payload bytes.Buffer
	payload.Write(headerBytes)
	payload.WriteByte('\n')
	payload.Write(queryBytes)
	payload.WriteByte('\n')

	// Use _msearch endpoint (the only POST endpoint allowed by Grafana's proxy)
	fullURL := c.buildURL("/_msearch")

	// Create the HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, &payload)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	// _msearch requires application/x-ndjson content type
	req.Header.Set("Content-Type", "application/x-ndjson")

	// Execute the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// Check for non-200 status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elasticsearch API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read the response body with a limit to prevent memory issues
	body := io.LimitReader(resp.Body, 1024*1024*48)
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Parse the _msearch response (contains array of responses)
	var msearchResponse MsearchResponse
	if err := json.Unmarshal(bodyBytes, &msearchResponse); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}

	// We only send one query, so we expect one response
	if len(msearchResponse.Responses) == 0 {
		return nil, fmt.Errorf("no responses returned from _msearch")
	}

	esResp := &msearchResponse.Responses[0]
	if esResp.Error != nil {
		return nil, fmt.Errorf("elasticsearch query error: %v", esResp.Error)
	}

	return esResp, nil
}

// buildElasticsearchQuery constructs an Elasticsearch query DSL JSON object
func buildElasticsearchQuery(query string, startTime, endTime *time.Time, size int) map[string]interface{} {
	esQuery := map[string]interface{}{
		"size": size,
		"sort": []map[string]interface{}{
			{
				"@timestamp": map[string]string{
					"order": "desc",
				},
			},
		},
	}

	// Build the query section
	var queryClause map[string]interface{}

	// If we have time range constraints, use a bool query with must clauses
	if startTime != nil || endTime != nil || query != "" {
		mustClauses := []map[string]interface{}{}

		// Add time range filter if provided
		if startTime != nil || endTime != nil {
			rangeQuery := map[string]interface{}{
				"@timestamp": map[string]interface{}{},
			}
			if startTime != nil {
				rangeQuery["@timestamp"].(map[string]interface{})["gte"] = startTime.Format(time.RFC3339)
			}
			if endTime != nil {
				rangeQuery["@timestamp"].(map[string]interface{})["lte"] = endTime.Format(time.RFC3339)
			}
			mustClauses = append(mustClauses, map[string]interface{}{
				"range": rangeQuery,
			})
		}

		// Add the user query if provided
		if query != "" {
			// Try to parse as JSON for Query DSL, otherwise treat as Lucene query string
			var parsedQuery map[string]interface{}
			if err := json.Unmarshal([]byte(query), &parsedQuery); err == nil {
				// It's valid JSON, use it directly
				mustClauses = append(mustClauses, parsedQuery)
			} else {
				// It's a Lucene query string
				mustClauses = append(mustClauses, map[string]interface{}{
					"query_string": map[string]interface{}{
						"query": query,
					},
				})
			}
		}

		queryClause = map[string]interface{}{
			"bool": map[string]interface{}{
				"must": mustClauses,
			},
		}
	} else {
		// No filters, match all
		queryClause = map[string]interface{}{
			"match_all": map[string]interface{}{},
		}
	}

	esQuery["query"] = queryClause

	return esQuery
}

// QueryElasticsearchParams defines the parameters for querying Elasticsearch
type QueryElasticsearchParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Elasticsearch datasource to query"`
	Index         string `json:"index" jsonschema:"required,description=The index pattern to search (e.g.\\, 'logs-*'\\, 'filebeat-*'\\, or a specific index name)"`
	Query         string `json:"query" jsonschema:"required,description=The search query. Can be either Lucene query syntax (e.g.\\, 'status:200 AND host:server1') or Elasticsearch Query DSL JSON (for advanced queries with aggregations)"`
	StartTime     string `json:"startTime,omitempty" jsonschema:"description=Optionally\\, the start time in RFC3339 format (e.g.\\, '2024-01-01T00:00:00Z'). Filters results to documents with @timestamp >= this value"`
	EndTime       string `json:"endTime,omitempty" jsonschema:"description=Optionally\\, the end time in RFC3339 format (e.g.\\, '2024-01-01T23:59:59Z'). Filters results to documents with @timestamp <= this value"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=10,description=Optionally\\, the maximum number of documents to return (max: 100\\, default: 10)"`
}

// queryElasticsearch executes a search query against an Elasticsearch datasource
func queryElasticsearch(ctx context.Context, args QueryElasticsearchParams) ([]ElasticsearchDocument, error) {
	client, err := newElasticsearchClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Elasticsearch client: %w", err)
	}

	// Parse time range if provided
	var startTime, endTime *time.Time
	if args.StartTime != "" {
		t, err := time.Parse(time.RFC3339, args.StartTime)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		startTime = &t
	}
	if args.EndTime != "" {
		t, err := time.Parse(time.RFC3339, args.EndTime)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		endTime = &t
	}

	// Apply limit constraints
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultElasticsearchLimit
	}
	if limit > MaxElasticsearchLimit {
		limit = MaxElasticsearchLimit
	}

	// Execute the search
	response, err := client.search(ctx, args.Index, args.Query, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}

	// Convert hits to documents
	documents := make([]ElasticsearchDocument, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		doc := ElasticsearchDocument{
			Source: make(map[string]interface{}),
		}

		if index, ok := hit["_index"].(string); ok {
			doc.Index = index
		}
		if id, ok := hit["_id"].(string); ok {
			doc.ID = id
		}

		if score, ok := hit["_score"].(float64); ok {
			doc.Score = &score
		}

		if source, ok := hit["_source"].(map[string]interface{}); ok {
			doc.Source = source
			// Extract timestamp if present (can be string or numeric epoch millis)
			switch ts := source["@timestamp"].(type) {
			case string:
				doc.Timestamp = ts
			case float64:
				// Convert epoch milliseconds to RFC3339
				sec := int64(ts) / 1000
				nsec := (int64(ts) % 1000) * int64(time.Millisecond)
				doc.Timestamp = time.Unix(sec, nsec).UTC().Format(time.RFC3339Nano)
			}
		}

		if fields, ok := hit["fields"].(map[string]interface{}); ok {
			doc.Fields = fields
		}

		documents = append(documents, doc)
	}

	return documents, nil
}

// QueryElasticsearch is a tool for querying Elasticsearch datasources
var QueryElasticsearch = mcpgrafana.MustTool(
	"query_elasticsearch",
	"Executes a search query against an Elasticsearch datasource and retrieves matching documents. Supports both Lucene query syntax (e.g., 'status:200 AND host:server1') and Elasticsearch Query DSL JSON for complex queries. Returns a list of documents with their index, ID, source fields, and optional score. Use this to search logs, metrics, or any indexed data stored in Elasticsearch. Defaults to 10 results and sorts by @timestamp in descending order (newest first).",
	queryElasticsearch,
	mcp.WithTitleAnnotation("Query Elasticsearch"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddElasticsearchTools registers all Elasticsearch tools with the MCP server
func AddElasticsearchTools(mcp *server.MCPServer) {
	QueryElasticsearch.Register(mcp)
}
