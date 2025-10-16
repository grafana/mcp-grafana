package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"

	mcpgrafana "github.com/grafana/mcp-grafana"
)

// DataSourceQuery represents a single query within a datasource request
type DataSourceQuery struct {
	RefID         string        `json:"refId" jsonschema:"required,description=Reference ID for the query\\, used to identify this query in the response"`
	Datasource    DataSourceRef `json:"datasource" jsonschema:"required,description=The datasource configuration to query"`
	Target        string        `json:"target,omitempty" jsonschema:"description=The query target/expression (e.g.\\, PromQL for Prometheus\\, Graphite expression for Graphite)"`
	Expr          string        `json:"expr,omitempty" jsonschema:"description=Alternative field for query expression\\, commonly used by Prometheus"`
	IntervalMs    int           `json:"intervalMs,omitempty" jsonschema:"description=The suggested interval between data points in milliseconds"`
	MaxDataPoints int           `json:"maxDataPoints,omitempty" jsonschema:"description=The maximum number of data points to return"`
	Format        string        `json:"format,omitempty" jsonschema:"description=The format of the response data (e.g.\\, 'time_series'\\, 'table'\\, 'logs')"`
	Hide          bool          `json:"hide,omitempty" jsonschema:"description=Whether to hide this query in the UI"`
	QueryType     string        `json:"queryType,omitempty" jsonschema:"description=The type of query (e.g.\\, 'range'\\, 'instant' for Prometheus)"`
	Exemplar      bool          `json:"exemplar,omitempty" jsonschema:"description=Whether to return exemplar data (Prometheus specific)"`
	// Additional fields for specific datasource types can be added as needed
	RawSQL   string `json:"rawSql,omitempty" jsonschema:"description=Raw SQL query for SQL-based datasources"`
	Database string `json:"database,omitempty" jsonschema:"description=Database name for SQL datasources"`
	Table    string `json:"table,omitempty" jsonschema:"description=Table name for SQL datasources"`
}

// DataSourceRef represents a reference to a datasource
type DataSourceRef struct {
	Type string `json:"type" jsonschema:"required,description=The type of the datasource (e.g.\\, 'prometheus'\\, 'graphite'\\, 'loki'\\, 'mysql'\\, 'postgres')"`
	UID  string `json:"uid" jsonschema:"required,description=The unique identifier of the datasource"`
	Name string `json:"name,omitempty" jsonschema:"description=The name of the datasource (optional)"`
}

// QueryDataSourceParams represents the parameters for querying a datasource
// Not Using MetricRequest in openapi-client
type QueryDataSourceParams struct {
	Queries []DataSourceQuery `json:"queries" jsonschema:"required,description=Array of queries to execute against datasources. Each query can target different datasources and use different query languages (PromQL\\, Graphite\\, SQL\\, etc.)"`
	From    string            `json:"from" jsonschema:"required,description=Start time for the query. Supports relative time (e.g.\\, 'now-5m'\\, 'now-1h'\\, 'now-1d') or absolute time in RFC3339 format"`
	To      string            `json:"to" jsonschema:"required,description=End time for the query. Supports relative time (e.g.\\, 'now'\\, 'now-1h') or absolute time in RFC3339 format"`
}

// api/ds/query api wrapper
func dsQuery(ctx context.Context, params QueryDataSourceParams) (interface{}, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)

	// Convert params to the expected API format
	apiRequest := map[string]interface{}{
		"queries": params.Queries,
		"from":    params.From,
		"to":      params.To,
	}

	// Marshal the request body
	requestBody, err := json.Marshal(apiRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create HTTP request directly to get raw JSON response
	url := fmt.Sprintf("%s/api/ds/query", cfg.URL)
	slog.Debug("dsQuery", "url", url, "body", string(requestBody))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")

	// Create HTTP client with TLS config and authentication
	var transport http.RoundTripper = http.DefaultTransport
	if cfg.TLSConfig != nil {
		var err error
		transport, err = cfg.TLSConfig.HTTPTransport(transport.(*http.Transport))
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS transport: %w", err)
		}
	}

	// Add authentication to transport
	transport = NewAuthRoundTripper(transport, cfg.AccessToken, cfg.IDToken, cfg.APIKey, cfg.BasicAuth)

	// Wrap with org ID support
	transport = mcpgrafana.NewOrgIDRoundTripper(transport, cfg.OrgID)

	client := &http.Client{
		Transport: mcpgrafana.NewUserAgentTransport(transport),
	}

	// Make the request
	// github.com/grafana/grafana-openapi-client-go/client/ds QueryMetricsWithExpressionsWithParams
	// only returns meta info, so we use raw http client
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read the raw response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	slog.Debug("dsQuery", "response", string(responseBody))

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(responseBody))
	}

	// Parse as raw JSON to preserve the complete data structure
	var result interface{}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result, nil
}

var QueryDataSource = mcpgrafana.MustTool(
	"query_datasource",
	`Query a datasource using the /api/ds/query endpoint. This is a general-purpose tool 
	for querying any type of datasource supported by Grafana, including Prometheus, Graphite, Loki, 
	InfluxDB and others. 
	The tool supports multiple queries in a single request and flexible time range specifications.`,
	dsQuery,
	mcp.WithTitleAnnotation("Query a datasource"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)
