package tools

import (
	"context"
	"fmt"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// QueryElasticsearchParams defines the parameters for querying Elasticsearch or OpenSearch
type QueryElasticsearchParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Elasticsearch or OpenSearch datasource to query"`
	Index         string `json:"index" jsonschema:"required,description=The index pattern to search (e.g.\\, 'logs-*'\\, 'filebeat-*'\\, or a specific index name)"`
	Query         string `json:"query" jsonschema:"required,description=The search query. For Elasticsearch datasources\\, this can be either Lucene query syntax (e.g.\\, 'status:200 AND host:server1') or Elasticsearch Query DSL JSON (for advanced queries with aggregations). For OpenSearch datasources\\, only Lucene query syntax is supported."`
	StartTime     string `json:"startTime,omitempty" jsonschema:"description=Optionally\\, the start time. Filters results to documents with the configured time field (default @timestamp) >= this value. Supports RFC3339 (e.g. '2024-01-01T00:00:00Z')\\, relative to now (e.g. 'now'\\, 'now-1h'\\, 'now-30m')\\, or Unix timestamps"`
	EndTime       string `json:"endTime,omitempty" jsonschema:"description=Optionally\\, the end time. Filters results to documents with the configured time field (default @timestamp) <= this value. Supports RFC3339 (e.g. '2024-01-01T23:59:59Z')\\, relative to now (e.g. 'now'\\, 'now-1h')\\, or Unix timestamps"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=10,description=Optionally\\, the maximum number of documents to return (max: 100\\, default: 10)"`
}

// queryElasticsearch executes a search query against an Elasticsearch or OpenSearch datasource
func queryElasticsearch(ctx context.Context, args QueryElasticsearchParams) ([]ElasticsearchDocument, error) {
	backend, err := esBackendForDatasource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("getting backend: %w", err)
	}

	startTime, err := parseStartTime(args.StartTime)
	if err != nil {
		return nil, fmt.Errorf("parsing start time: %w", err)
	}

	endTime, err := parseEndTime(args.EndTime)
	if err != nil {
		return nil, fmt.Errorf("parsing end time: %w", err)
	}

	// Apply limit constraints
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	return backend.Search(ctx, args.Index, args.Query, startTime, endTime, limit)
}

// QueryElasticsearch is a tool for querying Elasticsearch and OpenSearch datasources
var QueryElasticsearch = mcpgrafana.MustTool(
	"query_elasticsearch",
	"Executes a search query against an Elasticsearch or OpenSearch datasource and retrieves matching documents. Supports Lucene query syntax (e.g., 'status:200 AND host:server1') for both Elasticsearch and OpenSearch. Elasticsearch Query DSL JSON is also supported for Elasticsearch datasources only (not OpenSearch). Returns a list of documents with their index, ID, source fields, and optional score. Use this to search logs, metrics, or any indexed data stored in Elasticsearch or OpenSearch. Defaults to 10 results and sorts by @timestamp in descending order (newest first).",
	queryElasticsearch,
	mcp.WithTitleAnnotation("Query Elasticsearch"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddElasticsearchTools registers all Elasticsearch and OpenSearch tools with the MCP server
func AddElasticsearchTools(mcp *server.MCPServer) {
	QueryElasticsearch.Register(mcp)
}
