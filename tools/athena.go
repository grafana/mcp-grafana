package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// DefaultAthenaLimit is the default number of rows to return if not specified.
	DefaultAthenaLimit = 100

	// MaxAthenaLimit is the maximum number of rows that can be returned.
	MaxAthenaLimit = 1000

	// AthenaDatasourceType is the Grafana datasource type identifier for Athena.
	AthenaDatasourceType = "grafana-athena-datasource"

	// AthenaFormatTable requests table-formatted results from the Athena plugin.
	AthenaFormatTable = 1

	// athenaResponseLimitBytes is the maximum response size (10MB) before truncation.
	athenaResponseLimitBytes = 1024 * 1024 * 10
)

var (
	athenaTimeFilterRe = regexp.MustCompile(`\$__timeFilter\(([^)]+)\)`)
	athenaDateFilterRe = regexp.MustCompile(`\$__dateFilter\(([^)]+)\)`)
	athenaUnixFilterRe = regexp.MustCompile(`\$__unixEpochFilter\(([^)]+)\)`)
	athenaLimitRe      = regexp.MustCompile(`(?i)\bLIMIT\s+(\d+)`)
)

type athenaClient struct {
	httpClient *http.Client
	baseURL    string
	uid        string
}

func newAthenaClient(ctx context.Context, uid string) (*athenaClient, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	if ds.Type != AthenaDatasourceType {
		return nil, fmt.Errorf("datasource %s is of type %s, not %s", uid, ds.Type, AthenaDatasourceType)
	}

	cfg := mcpgrafana.GrafanaConfigFromContext(ctx)
	baseURL := strings.TrimRight(cfg.URL, "/")

	transport, err := mcpgrafana.BuildTransport(&cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	client := &http.Client{
		Transport: transport,
	}

	return &athenaClient{
		httpClient: client,
		baseURL:    baseURL,
		uid:        uid,
	}, nil
}

// resource makes a POST request to the Athena plugin's resource API.
func (c *athenaClient) resource(ctx context.Context, path string, body map[string]string) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling resource request: %w", err)
	}

	url := c.baseURL + "/api/datasources/uid/" + c.uid + "/resources" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Athena resource %s returned status %d: %s", path, resp.StatusCode, string(errBody))
	}

	limitedBody := io.LimitReader(resp.Body, int64(athenaResponseLimitBytes))
	return io.ReadAll(limitedBody)
}

type athenaQueryResponse struct {
	Results map[string]struct {
		Status int `json:"status,omitempty"`
		Frames []struct {
			Schema struct {
				Name   string `json:"name,omitempty"`
				RefID  string `json:"refId,omitempty"`
				Fields []struct {
					Name     string `json:"name"`
					Type     string `json:"type"`
					TypeInfo struct {
						Frame string `json:"frame,omitempty"`
					} `json:"typeInfo,omitempty"`
				} `json:"fields"`
			} `json:"schema"`
			Data struct {
				Values [][]interface{} `json:"values"`
			} `json:"data"`
		} `json:"frames,omitempty"`
		Error string `json:"error,omitempty"`
	} `json:"results"`
}

func (c *athenaClient) query(ctx context.Context, rawSQL string, from, to time.Time, connectionArgs map[string]interface{}) (*athenaQueryResponse, error) {
	queryMap := map[string]interface{}{
		"datasource": map[string]string{
			"uid":  c.uid,
			"type": AthenaDatasourceType,
		},
		"rawSql":         rawSQL,
		"refId":          "A",
		"format":         AthenaFormatTable,
		"connectionArgs": connectionArgs,
	}

	payload := map[string]interface{}{
		"queries": []map[string]interface{}{queryMap},
		"from":    strconv.FormatInt(from.UnixMilli(), 10),
		"to":      strconv.FormatInt(to.UnixMilli(), 10),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query payload: %w", err)
	}

	url := c.baseURL + "/api/ds/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Athena query returned status %d: %s", resp.StatusCode, string(errBody))
	}

	limitedBody := io.LimitReader(resp.Body, int64(athenaResponseLimitBytes))
	respBytes, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var queryResp athenaQueryResponse
	if err := unmarshalJSONWithLimitMsg(respBytes, &queryResp, athenaResponseLimitBytes); err != nil {
		return nil, err
	}

	return &queryResp, nil
}

func substituteAthenaMacros(query string, from, to time.Time) string {
	fromTS := from.UTC().Format("2006-01-02 15:04:05")
	toTS := to.UTC().Format("2006-01-02 15:04:05")
	fromDate := from.UTC().Format("2006-01-02")
	toDate := to.UTC().Format("2006-01-02")
	fromUnix := from.UTC().Unix()
	toUnix := to.UTC().Unix()
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()
	rangeSeconds := toUnix - fromUnix
	intervalSeconds := rangeSeconds / 1000
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}

	query = athenaTimeFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := athenaTimeFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			col := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s BETWEEN TIMESTAMP '%s' AND TIMESTAMP '%s'", col, fromTS, toTS)
		}
		return match
	})

	query = athenaDateFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := athenaDateFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			col := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s BETWEEN date '%s' AND date '%s'", col, fromDate, toDate)
		}
		return match
	})

	query = athenaUnixFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := athenaUnixFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			col := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s BETWEEN %d AND %d", col, fromUnix, toUnix)
		}
		return match
	})

	// $__timeFrom() -> TIMESTAMP '...'
	query = strings.ReplaceAll(query, "$__timeFrom()", fmt.Sprintf("TIMESTAMP '%s'", fromTS))

	// $__timeTo() -> TIMESTAMP '...'
	query = strings.ReplaceAll(query, "$__timeTo()", fmt.Sprintf("TIMESTAMP '%s'", toTS))

	// $__from -> Unix milliseconds (must come after $__timeFrom to avoid partial match)
	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))

	// $__to -> Unix milliseconds (must come after $__timeTo to avoid partial match)
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))

	// $__interval_ms -> interval in milliseconds (must be before $__interval to avoid partial replacement)
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))

	// $__interval -> interval string
	query = strings.ReplaceAll(query, "$__interval", fmt.Sprintf("%ds", intervalSeconds))

	return query
}

func enforceAthenaLimit(query string, requestedLimit int) string {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "SHOW") || strings.HasPrefix(upper, "DESCRIBE") {
		return query
	}

	limit := requestedLimit
	if limit <= 0 {
		limit = DefaultAthenaLimit
	}
	if limit > MaxAthenaLimit {
		limit = MaxAthenaLimit
	}

	if athenaLimitRe.MatchString(query) {
		query = athenaLimitRe.ReplaceAllStringFunc(query, func(match string) string {
			submatch := athenaLimitRe.FindStringSubmatch(match)
			if len(submatch) > 1 {
				existingLimit, _ := strconv.Atoi(submatch[1])
				if existingLimit > MaxAthenaLimit {
					return fmt.Sprintf("LIMIT %d", MaxAthenaLimit)
				}
			}
			return match
		})
		return query
	}

	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")
	return fmt.Sprintf("%s LIMIT %d", query, limit)
}

type ListAthenaCatalogsParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Athena datasource. Use list_datasources to find available UIDs."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region override (e.g. us-east-1). Defaults to datasource config."`
}

func listAthenaCatalogs(ctx context.Context, args ListAthenaCatalogsParams) ([]string, error) {
	client, err := newAthenaClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Athena client: %w", err)
	}

	body := map[string]string{}
	if args.Region != "" {
		body["region"] = args.Region
	}

	respBytes, err := client.resource(ctx, "/catalogs", body)
	if err != nil {
		return nil, err
	}

	var catalogs []string
	if err := unmarshalJSONWithLimitMsg(respBytes, &catalogs, athenaResponseLimitBytes); err != nil {
		return nil, err
	}
	return catalogs, nil
}

var ListAthenaCatalogs = mcpgrafana.MustTool(
	"list_athena_catalogs",
	"START HERE for Athena: List available data catalogs (e.g. AwsDataCatalog, Iceberg connectors). NEXT: Use list_athena_databases with a catalog.",
	listAthenaCatalogs,
	mcp.WithTitleAnnotation("List Athena catalogs"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListAthenaDatabasesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Athena datasource."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region override. Defaults to datasource config."`
	Catalog       string `json:"catalog,omitempty" jsonschema:"description=Data catalog name (e.g. AwsDataCatalog). Defaults to datasource config."`
}

func listAthenaDatabases(ctx context.Context, args ListAthenaDatabasesParams) ([]string, error) {
	client, err := newAthenaClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Athena client: %w", err)
	}

	body := map[string]string{}
	if args.Region != "" {
		body["region"] = args.Region
	}
	if args.Catalog != "" {
		body["catalog"] = args.Catalog
	}

	respBytes, err := client.resource(ctx, "/databases", body)
	if err != nil {
		return nil, err
	}

	var databases []string
	if err := unmarshalJSONWithLimitMsg(respBytes, &databases, athenaResponseLimitBytes); err != nil {
		return nil, err
	}
	return databases, nil
}

var ListAthenaDatabases = mcpgrafana.MustTool(
	"list_athena_databases",
	"List databases in an Athena catalog. Use after list_athena_catalogs. NEXT: Use list_athena_tables with a database.",
	listAthenaDatabases,
	mcp.WithTitleAnnotation("List Athena databases"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type ListAthenaTablesParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Athena datasource."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region override. Defaults to datasource config."`
	Catalog       string `json:"catalog,omitempty" jsonschema:"description=Data catalog name. Defaults to datasource config."`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name. Defaults to datasource config."`
}

func listAthenaTables(ctx context.Context, args ListAthenaTablesParams) ([]string, error) {
	client, err := newAthenaClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Athena client: %w", err)
	}

	body := map[string]string{}
	if args.Region != "" {
		body["region"] = args.Region
	}
	if args.Catalog != "" {
		body["catalog"] = args.Catalog
	}
	if args.Database != "" {
		body["database"] = args.Database
	}

	respBytes, err := client.resource(ctx, "/tables", body)
	if err != nil {
		return nil, err
	}

	var tables []string
	if err := unmarshalJSONWithLimitMsg(respBytes, &tables, athenaResponseLimitBytes); err != nil {
		return nil, err
	}
	return tables, nil
}

var ListAthenaTables = mcpgrafana.MustTool(
	"list_athena_tables",
	"List tables in an Athena database. Use after list_athena_databases. NEXT: Use describe_athena_table to see column schemas before querying.",
	listAthenaTables,
	mcp.WithTitleAnnotation("List Athena tables"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type DescribeAthenaTableParams struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the Athena datasource."`
	Table         string `json:"table" jsonschema:"required,description=Table name to describe."`
	Region        string `json:"region,omitempty" jsonschema:"description=AWS region override. Defaults to datasource config."`
	Catalog       string `json:"catalog,omitempty" jsonschema:"description=Data catalog name. Defaults to datasource config."`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name. Defaults to datasource config."`
}

func describeAthenaTable(ctx context.Context, args DescribeAthenaTableParams) ([]string, error) {
	if args.Table == "" {
		return nil, fmt.Errorf("table is required")
	}

	client, err := newAthenaClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Athena client: %w", err)
	}

	body := map[string]string{
		"table": args.Table,
	}
	if args.Region != "" {
		body["region"] = args.Region
	}
	if args.Catalog != "" {
		body["catalog"] = args.Catalog
	}
	if args.Database != "" {
		body["database"] = args.Database
	}

	respBytes, err := client.resource(ctx, "/columns", body)
	if err != nil {
		return nil, err
	}

	var columns []string
	if err := unmarshalJSONWithLimitMsg(respBytes, &columns, athenaResponseLimitBytes); err != nil {
		return nil, err
	}
	return columns, nil
}

var DescribeAthenaTable = mcpgrafana.MustTool(
	"describe_athena_table",
	"Get column names for an Athena table. Use after list_athena_tables. NEXT: Use query_athena with discovered column names.",
	describeAthenaTable,
	mcp.WithTitleAnnotation("Describe Athena table"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

type AthenaQueryParams struct {
	DatasourceUID              string            `json:"datasourceUid" jsonschema:"required,description=The UID of the Athena datasource. Use list_datasources to find available UIDs."`
	Query                      string            `json:"query" jsonschema:"required,description=Raw SQL query. Supports macros: $__timeFilter(column)\\, $__dateFilter(column)\\, $__unixEpochFilter(column)\\, $__timeFrom()\\, $__timeTo()\\, $__from/$__to (Unix ms)\\, $__interval\\, and ${varname} for variable substitution."`
	Start                      string            `json:"start,omitempty" jsonschema:"description=Start time. Formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: 1 hour ago."`
	End                        string            `json:"end,omitempty" jsonschema:"description=End time. Formats: 'now'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Default: now."`
	Region                     string            `json:"region,omitempty" jsonschema:"description=AWS region override. Defaults to datasource config."`
	Catalog                    string            `json:"catalog,omitempty" jsonschema:"description=Data catalog override. Defaults to datasource config."`
	Database                   string            `json:"database,omitempty" jsonschema:"description=Database override. Defaults to datasource config."`
	Variables                  map[string]string `json:"variables,omitempty" jsonschema:"description=Template variable substitutions as key-value pairs. Referenced as ${varname} or $varname in the query."`
	Limit                      int               `json:"limit,omitempty" jsonschema:"description=Appended as LIMIT when query has none (default: 100). Existing LIMIT clauses are capped at 1000 regardless of this parameter."`
	ResultReuseEnabled         bool              `json:"resultReuseEnabled,omitempty" jsonschema:"description=Enable Athena query result reuse to avoid redundant scans and reduce cost. Requires Athena engine version 3."`
	ResultReuseMaxAgeInMinutes int               `json:"resultReuseMaxAgeInMinutes,omitempty" jsonschema:"description=Maximum age in minutes for reused query results. Only applies when resultReuseEnabled is true."`
}

type AthenaQueryResult struct {
	Columns        []string                 `json:"columns"`
	Rows           []map[string]interface{} `json:"rows"`
	RowCount       int                      `json:"rowCount"`
	ProcessedQuery string                   `json:"processedQuery,omitempty"`
	Hints          *EmptyResultHints        `json:"hints,omitempty"`
}

func queryAthena(ctx context.Context, args AthenaQueryParams) (*AthenaQueryResult, error) {
	client, err := newAthenaClient(ctx, args.DatasourceUID)
	if err != nil {
		return nil, fmt.Errorf("creating Athena client: %w", err)
	}

	now := time.Now()
	fromTime := now.Add(-1 * time.Hour)
	toTime := now

	if args.Start != "" {
		parsed, err := parseStartTime(args.Start)
		if err != nil {
			return nil, fmt.Errorf("parsing start time: %w", err)
		}
		if !parsed.IsZero() {
			fromTime = parsed
		}
	}

	if args.End != "" {
		parsed, err := parseEndTime(args.End)
		if err != nil {
			return nil, fmt.Errorf("parsing end time: %w", err)
		}
		if !parsed.IsZero() {
			toTime = parsed
		}
	}

	processedQuery := args.Query
	processedQuery = substituteAthenaMacros(processedQuery, fromTime, toTime)
	processedQuery = substituteVariables(processedQuery, args.Variables)
	processedQuery = enforceAthenaLimit(processedQuery, args.Limit)

	connectionArgs := map[string]interface{}{}
	if args.Region != "" {
		connectionArgs["region"] = args.Region
	}
	if args.Catalog != "" {
		connectionArgs["catalog"] = args.Catalog
	}
	if args.Database != "" {
		connectionArgs["database"] = args.Database
	}
	if args.ResultReuseEnabled {
		connectionArgs["resultReuseEnabled"] = true
		if args.ResultReuseMaxAgeInMinutes > 0 {
			connectionArgs["resultReuseMaxAgeInMinutes"] = args.ResultReuseMaxAgeInMinutes
		}
	}

	resp, err := client.query(ctx, processedQuery, fromTime, toTime, connectionArgs)
	if err != nil {
		return nil, err
	}

	result := &AthenaQueryResult{
		Columns:        []string{},
		Rows:           []map[string]interface{}{},
		ProcessedQuery: processedQuery,
	}

	for refID, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("query error (refId=%s): %s", refID, r.Error)
		}

		for _, frame := range r.Frames {
			columns := make([]string, len(frame.Schema.Fields))
			for i, field := range frame.Schema.Fields {
				columns[i] = field.Name
			}
			result.Columns = columns

			if len(frame.Data.Values) == 0 {
				continue
			}

			rowCount := len(frame.Data.Values[0])
			for i := 0; i < rowCount; i++ {
				row := make(map[string]interface{})
				for colIdx, colName := range columns {
					if colIdx < len(frame.Data.Values) && i < len(frame.Data.Values[colIdx]) {
						row[colName] = frame.Data.Values[colIdx][i]
					}
				}
				result.Rows = append(result.Rows, row)
			}
		}
	}

	result.RowCount = len(result.Rows)

	if result.RowCount == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: "athena",
			Query:          args.Query,
			ProcessedQuery: processedQuery,
			StartTime:      fromTime,
			EndTime:        toTime,
		})
	}

	return result, nil
}

var QueryAthena = mcpgrafana.MustTool(
	"query_athena",
	`Query Amazon Athena via Grafana. REQUIRED FIRST: Use list_athena_catalogs -> list_athena_databases -> list_athena_tables -> describe_athena_table to discover schema, then query.

Supports macros: $__timeFilter(column), $__dateFilter(column), $__unixEpochFilter(column), $__timeFrom(), $__timeTo(), $__from, $__to, $__interval, ${varname}

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)

Athena queries are async — Grafana handles polling. Use LIMIT and partition-aware WHERE clauses to avoid timeouts on large tables.

Example: SELECT request_time, status FROM my_table WHERE $__timeFilter(request_time) LIMIT 100`,
	queryAthena,
	mcp.WithTitleAnnotation("Query Athena"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddAthenaTools registers all Athena tools with the MCP server.
func AddAthenaTools(s *server.MCPServer) {
	ListAthenaCatalogs.Register(s)
	ListAthenaDatabases.Register(s)
	ListAthenaTables.Register(s)
	DescribeAthenaTable.Register(s)
	QueryAthena.Register(s)
}
