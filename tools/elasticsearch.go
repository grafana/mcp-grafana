package tools

import (
	"context"
	"fmt"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// QueryElasticsearchParams defines the parameters for querying Elasticsearch or OpenSearch
type QueryElasticsearchParams struct {
	DatasourceUID string   `json:"datasourceUid" jsonschema:"required,description=The UID of the Elasticsearch or OpenSearch datasource to query"`
	Index         string   `json:"index" jsonschema:"required,description=The index pattern to search (e.g.\\, 'logs-*'\\, 'filebeat-*'\\, or a specific index name)"`
	Query         string   `json:"query" jsonschema:"required,description=The search query. For Elasticsearch datasources\\, this can be either Lucene query syntax (e.g.\\, 'status:200 AND host:server1') or Elasticsearch Query DSL JSON (for advanced queries with aggregations). For OpenSearch datasources\\, only Lucene query syntax is supported."`
	StartTime     string   `json:"startTime,omitempty" jsonschema:"description=Optionally\\, the start time. Filters results to documents with the configured time field (default @timestamp) >= this value. Supports RFC3339 (e.g. '2024-01-01T00:00:00Z')\\, relative to now (e.g. 'now'\\, 'now-1h'\\, 'now-30m')\\, or Unix ms (e.g. '1738519200000')"`
	EndTime       string   `json:"endTime,omitempty" jsonschema:"description=Optionally\\, the end time. Filters results to documents with the configured time field (default @timestamp) <= this value. Supports RFC3339 (e.g. '2024-01-01T23:59:59Z')\\, relative to now (e.g. 'now'\\, 'now-1h')\\, or Unix ms (e.g. '1738522800000')"`
	Limit         int      `json:"limit,omitempty" jsonschema:"default=10,description=Optionally\\, the maximum number of documents to return (max: 100\\, default: 10)"`
	Format        string   `json:"format,omitempty" jsonschema:"enum=full,enum=compact,description=Output format: 'full' (default) returns whole documents; 'compact' returns only the timestamp and the log message and level fields configured on the datasource (message defaults to 'message')"`
	Fields        []string `json:"fields,omitempty" jsonschema:"description=Optionally\\, the source fields to return\\, dotted for nested ones (e.g. 'kubernetes.pod_name'). 'full' keeps only these; 'compact' adds them"`
}

// queryElasticsearch executes a search query against an Elasticsearch or OpenSearch datasource
func queryElasticsearch(ctx context.Context, args QueryElasticsearchParams) ([]ElasticsearchDocument, error) {
	format := strings.ToLower(strings.TrimSpace(args.Format))
	switch format {
	case "", "full", "compact":
	default:
		return nil, fmt.Errorf("invalid format %q: must be 'full' or 'compact'", args.Format)
	}

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

	docs, err := backend.Search(ctx, args.Index, args.Query, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}

	fields := args.Fields
	if format == "compact" {
		fields = append(backend.logFields().names(), fields...)
	} else if len(fields) == 0 {
		return docs, nil
	}
	for i := range docs {
		docs[i].Source = selectSourceFields(docs[i].Source, fields)
		if format == "compact" {
			// Compact results hold the timestamp and the selected source fields.
			docs[i].Index, docs[i].ID, docs[i].Score, docs[i].Fields = "", "", nil, nil
		}
	}
	return docs, nil
}

// QueryElasticsearch is a tool for querying Elasticsearch and OpenSearch datasources
var QueryElasticsearch = mcpgrafana.MustTool(
	"query_elasticsearch",
	"Executes a search query against an Elasticsearch or OpenSearch datasource and retrieves matching documents. Supports Lucene query syntax (e.g., 'status:200 AND host:server1') for both Elasticsearch and OpenSearch. Elasticsearch Query DSL JSON is also supported for Elasticsearch datasources only (not OpenSearch). Returns a list of documents with their index, ID, source fields, and optional score. Use this to search logs, metrics, or any indexed data stored in Elasticsearch or OpenSearch. Defaults to 10 results and sorts by @timestamp in descending order (newest first). For log searches, use `format` 'compact' or `fields` to shrink the response.",
	queryElasticsearch,
	mcpgrafana.WithTitleAnnotation("Query Elasticsearch"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

// AddElasticsearchTools registers all Elasticsearch and OpenSearch tools with the MCP server.
// Every tool in this category executes a query, so nothing is registered when
// enableQueryTools is false.
func AddElasticsearchTools(s *mcp.Server, enableQueryTools bool) {
	if enableQueryTools {
		QueryElasticsearch.Register(s)
	}
}
