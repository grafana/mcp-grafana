package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const quickwitDatasourceType = "quickwit-quickwit-datasource"

type quickwitBackend struct {
	httpClient            *http.Client
	baseURL               string
	configuredIndex       string
	timeField             string
	timestampOutputFormat string
	metadataFetched       bool
}

type quickwitIndexMetadata struct {
	IndexConfig struct {
		IndexID    string `json:"index_id"`
		DocMapping struct {
			TimestampField string                 `json:"timestamp_field"`
			FieldMappings  []quickwitFieldMapping `json:"field_mappings"`
		} `json:"doc_mapping"`
	} `json:"index_config"`
}

type quickwitFieldMapping struct {
	Name          string                 `json:"name"`
	Type          string                 `json:"type"`
	OutputFormat  *string                `json:"output_format"`
	FieldMappings []quickwitFieldMapping `json:"field_mappings"`
}

func indexFromQuickwitDataSource(ds *models.DataSource) string {
	if jsonData, ok := ds.JSONData.(map[string]interface{}); ok {
		if idx, ok := jsonData["index"].(string); ok && idx != "" {
			return idx
		}
	}
	if ds.Database != "" {
		return ds.Database
	}
	return ""
}

func newQuickwitBackend(ctx context.Context, ds *models.DataSource) (*quickwitBackend, error) {
	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	proxyURL := fmt.Sprintf("%s/api/datasources/proxy/uid/%s", strings.TrimRight(cfg.URL, "/"), ds.UID)

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create custom transport: %w", err)
	}

	return &quickwitBackend{
		httpClient:      &http.Client{Transport: transport, CheckRedirect: refuseRedirect},
		baseURL:         proxyURL,
		configuredIndex: indexFromQuickwitDataSource(ds),
	}, nil
}

func quickwitBackendForDatasource(ctx context.Context, uid string) (*quickwitBackend, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}
	if ds.Type != quickwitDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not quickwit", uid, ds.Type)
	}
	return newQuickwitBackend(ctx, ds)
}

func (b *quickwitBackend) resolveIndex(index string) (string, error) {
	if index != "" {
		return index, nil
	}
	if b.configuredIndex == "" {
		return "", fmt.Errorf("no index specified and datasource has no configured index")
	}
	return b.configuredIndex, nil
}

func (b *quickwitBackend) Search(ctx context.Context, index, query string, startTime, endTime time.Time, limit int) ([]ElasticsearchDocument, error) {
	resolvedIndex, err := b.resolveIndex(index)
	if err != nil {
		return nil, err
	}

	if b.configuredIndex != "" && !indexMatchesPattern(b.configuredIndex, resolvedIndex) {
		return nil, fmt.Errorf("the requested index %q is not compatible with this datasource's configured index pattern %q; use an index that matches the pattern or choose a different datasource", resolvedIndex, b.configuredIndex)
	}

	if err := b.ensureTimestampInfo(ctx, resolvedIndex); err != nil {
		return nil, err
	}

	searchQuery := esSearchQuery{
		query:      query,
		startTime:  startTime,
		endTime:    endTime,
		size:       limit,
		timeField:  b.timeField,
		sortFormat: quickwitTimestampSortFormat(b.timestampOutputFormat),
	}.build()
	return executeMSearch(ctx, b.httpClient, buildURL(b.baseURL, "_elastic/_msearch"), resolvedIndex, searchQuery, b.timeField)
}

func (b *quickwitBackend) ensureTimestampInfo(ctx context.Context, index string) error {
	if b.metadataFetched {
		return nil
	}

	timeField, outputFormat, err := b.fetchTimestampInfo(ctx, index)
	if err != nil {
		return err
	}

	b.timeField = timeField
	b.timestampOutputFormat = outputFormat
	b.metadataFetched = true
	return nil
}

func (b *quickwitBackend) fetchTimestampInfo(ctx context.Context, index string) (string, string, error) {
	metadataURL := buildURL(b.baseURL, "indexes") + "?" + url.Values{"index_id_patterns": {index}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("creating metadata request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetching index metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", "", fmt.Errorf("quickwit metadata API returned status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := readResponseBody(resp.Body, defaultResponseLimitBytes)
	if err != nil {
		return "", "", fmt.Errorf("reading metadata response: %w", err)
	}

	var metadata []quickwitIndexMetadata
	if err := json.Unmarshal(bodyBytes, &metadata); err != nil {
		return "", "", fmt.Errorf("unmarshaling index metadata: %w", err)
	}

	return timestampInfoFromMetadata(metadata)
}

func timestampInfoFromMetadata(metadata []quickwitIndexMetadata) (string, string, error) {
	if len(metadata) == 0 {
		return "", "", fmt.Errorf("index metadata list is empty")
	}

	refTimeField, refOutputFormat := findTimestampFieldInfo(metadata[0])
	if refTimeField == "" {
		return "", "", fmt.Errorf("index %q has no timestamp field configured", metadata[0].IndexConfig.IndexID)
	}

	for _, item := range metadata[1:] {
		timeField, outputFormat := findTimestampFieldInfo(item)
		if timeField != refTimeField || outputFormat != refOutputFormat {
			return "", "", fmt.Errorf("indexes matching pattern have incompatible timestamp fields, found: %s (%s) and %s (%s)", refTimeField, refOutputFormat, timeField, outputFormat)
		}
	}

	return refTimeField, refOutputFormat, nil
}

func findTimestampFieldInfo(metadata quickwitIndexMetadata) (string, string) {
	timeField := metadata.IndexConfig.DocMapping.TimestampField
	outputFormat, _ := findQuickwitTimestampFormat(timeField, nil, metadata.IndexConfig.DocMapping.FieldMappings)
	return timeField, outputFormat
}

func findQuickwitTimestampFormat(timestampFieldName string, parentName *string, fieldMappings []quickwitFieldMapping) (string, bool) {
	for _, field := range fieldMappings {
		fieldName := field.Name
		if parentName != nil {
			fieldName = fmt.Sprintf("%s.%s", *parentName, fieldName)
		}
		if field.Type == "datetime" && fieldName == timestampFieldName && field.OutputFormat != nil {
			return *field.OutputFormat, true
		}
		if field.Type == "object" && len(field.FieldMappings) > 0 {
			if result, found := findQuickwitTimestampFormat(timestampFieldName, &fieldName, field.FieldMappings); found {
				return result, true
			}
		}
	}
	return "", false
}

func quickwitTimestampSortFormat(outputFormat string) string {
	if strings.Contains(outputFormat, "nanos") {
		return "epoch_nanos_int"
	}
	return ""
}

// QueryQuickwitParams defines the parameters for querying Quickwit.
type QueryQuickwitParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Quickwit datasource to query"`
	Index         string `json:"index,omitempty" jsonschema:"description=Optionally\\, the index or index pattern to search. Defaults to the index configured on the datasource (jsonData.index)"`
	Query         string `json:"query" jsonschema:"required,description=The search query in Lucene syntax (e.g.\\, 'severity_text:ERROR AND service_name:api') or partial Elasticsearch-compatible Query DSL JSON"`
	StartTime     string `json:"startTime,omitempty" jsonschema:"description=Optionally\\, the start time. Filters results to documents with timestamp >= this value. Supports RFC3339 (e.g. '2024-01-01T00:00:00Z')\\, relative to now (e.g. 'now'\\, 'now-1h'\\, 'now-30m')\\, or Unix timestamps"`
	EndTime       string `json:"endTime,omitempty" jsonschema:"description=Optionally\\, the end time. Filters results to documents with timestamp <= this value. Supports RFC3339 (e.g. '2024-01-01T23:59:59Z')\\, relative to now (e.g. 'now'\\, 'now-1h')\\, or Unix timestamps"`
	Limit         int    `json:"limit,omitempty" jsonschema:"default=10,description=Optionally\\, the maximum number of documents to return (max: 100\\, default: 10)"`
}

func queryQuickwit(ctx context.Context, args QueryQuickwitParams) ([]ElasticsearchDocument, error) {
	backend, err := quickwitBackendForDatasource(ctx, args.DatasourceUID)
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

	limit := args.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	return backend.Search(ctx, args.Index, args.Query, startTime, endTime, limit)
}

// QueryQuickwit is a tool for querying Quickwit datasources.
var QueryQuickwit = mcpgrafana.MustTool(
	"query_quickwit",
	"Executes a search query against a Quickwit datasource and retrieves matching documents. Supports Lucene query syntax (e.g., 'severity_text:ERROR AND service_name:api') and partial Elasticsearch-compatible Query DSL JSON. The timestamp field is resolved from Quickwit index metadata (not jsonData.timeField). Returns a list of documents with their index, ID, source fields, and optional score. Use this to search logs or other indexed data stored in Quickwit. Defaults to 10 results and sorts by the index timestamp field in descending order (newest first).",
	queryQuickwit,
	mcp.WithTitleAnnotation("Query Quickwit"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddQuickwitTools registers all Quickwit tools with the MCP server.
func AddQuickwitTools(mcp *server.MCPServer) {
	QueryQuickwit.Register(mcp)
}
