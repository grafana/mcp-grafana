package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/tools/sql"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// queryRunner implements sql.QueryRunner using the tools-internal HTTP helpers.
type queryRunner struct {
	client  *http.Client
	baseURL string
}

func newQueryRunner(ctx context.Context) (sql.QueryRunner, error) {
	client, baseURL, err := newDSQueryHTTPClient(ctx)
	if err != nil {
		return nil, err
	}
	return &queryRunner{client: client, baseURL: baseURL}, nil
}

func (qr *queryRunner) RunSQL(ctx context.Context, uid, dsType string, rawSQL string, format interface{}, from, to time.Time, extra map[string]interface{}) ([]string, []map[string]interface{}, error) {
	query := map[string]interface{}{
		"datasource": map[string]string{
			"uid":  uid,
			"type": dsType,
		},
		"rawSql": rawSQL,
		"refId":  "A",
		"format": format,
	}
	for k, v := range extra {
		query[k] = v
	}

	payload := dsQueryPayload(from, to, query)
	resp, err := doDSQuery(ctx, qr.client, qr.baseURL, payload)
	if err != nil {
		return nil, nil, err
	}
	return framesToTabularRows(resp)
}

func (qr *queryRunner) ResourceRequest(ctx context.Context, uid, path string, body map[string]string) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling resource request: %w", err)
	}

	url := qr.baseURL + "/api/datasources/uid/" + uid + "/resources" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := qr.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("resource %s returned status %d: %s", path, resp.StatusCode, string(errBody))
	}

	const maxResourceResponseBytes = 10 * 1024 * 1024
	return readResponseBody(resp.Body, maxResourceResponseBytes)
}

func jsonDataString(ds *models.DataSource, key string) string {
	if ds.JSONData == nil {
		return ""
	}
	raw, ok := ds.JSONData.(map[string]interface{})
	if !ok {
		return ""
	}
	if v, ok := raw[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func fillDatasourceDefaults(ds *models.DataSource, catalog, database, region *string) {
	if catalog != nil && *catalog == "" {
		if v := jsonDataString(ds, "catalog"); v != "" {
			*catalog = v
		}
	}
	if database != nil && *database == "" {
		if ds.Database != "" {
			*database = ds.Database
		}
		if *database == "" {
			if v := jsonDataString(ds, "database"); v != "" {
				*database = v
			}
		}
		if *database == "" {
			if v := jsonDataString(ds, "defaultDatabase"); v != "" {
				*database = v
			}
		}
	}
	if region != nil && *region == "" {
		if v := jsonDataString(ds, "defaultRegion"); v != "" {
			*region = v
		}
	}
}

func querySQLHandler(ctx context.Context, args sql.QuerySQLParams) (*sql.SQLQueryResult, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("looking up datasource: %w", err)
	}

	dialect, err := sql.DialectFor(ds.Type)
	if err != nil {
		return nil, err
	}

	fillDatasourceDefaults(ds, &args.Catalog, &args.Database, &args.Region)

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
	processedQuery = dialect.SubstituteMacros(processedQuery, fromTime, toTime)
	processedQuery = substituteVariables(processedQuery, args.Variables)
	processedQuery = dialect.EnforceLimit(processedQuery, args.Limit)

	qr, err := newQueryRunner(ctx)
	if err != nil {
		return nil, err
	}

	extra := dialect.ExtraQueryPayloadFields(args)

	columns, rows, err := qr.RunSQL(ctx, args.DatasourceUID, ds.Type, processedQuery, dialect.DefaultFormat(), fromTime, toTime, extra)
	if err != nil {
		return nil, err
	}

	result := &sql.SQLQueryResult{
		Columns:        columns,
		Rows:           rows,
		RowCount:       len(rows),
		ProcessedQuery: processedQuery,
	}

	if result.RowCount == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: normalizeDatasourceType(ds.Type),
			Query:          args.Query,
			ProcessedQuery: processedQuery,
			StartTime:      fromTime,
			EndTime:        toTime,
		})
	}

	return result, nil
}

func listSQLDatabasesHandler(ctx context.Context, args sql.ListSQLDatabasesParams) (*sql.ListSQLDatabasesResult, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("looking up datasource: %w", err)
	}

	dialect, err := sql.DialectFor(ds.Type)
	if err != nil {
		return nil, err
	}

	qr, err := newQueryRunner(ctx)
	if err != nil {
		return nil, err
	}

	return dialect.ListDatabases(ctx, qr, ds.Type, args)
}

func listSQLTablesHandler(ctx context.Context, args sql.ListSQLTablesParams) (*sql.ListSQLTablesResult, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("looking up datasource: %w", err)
	}

	dialect, err := sql.DialectFor(ds.Type)
	if err != nil {
		return nil, err
	}

	qr, err := newQueryRunner(ctx)
	if err != nil {
		return nil, err
	}

	fillDatasourceDefaults(ds, &args.Catalog, &args.Database, &args.Region)

	return dialect.ListTables(ctx, qr, ds.Type, args)
}

func describeSQLTableHandler(ctx context.Context, args sql.DescribeSQLTableParams) (*sql.DescribeSQLTableResult, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: args.DatasourceUID})
	if err != nil {
		return nil, fmt.Errorf("looking up datasource: %w", err)
	}

	dialect, err := sql.DialectFor(ds.Type)
	if err != nil {
		return nil, err
	}

	qr, err := newQueryRunner(ctx)
	if err != nil {
		return nil, err
	}

	fillDatasourceDefaults(ds, &args.Catalog, &args.Database, &args.Region)

	return dialect.DescribeTable(ctx, qr, ds.Type, args)
}

var QuerySQL = mcpgrafana.MustTool(
	"query_sql",
	`Query a supported SQL datasource (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL) via Grafana.

REQUIRED FIRST: Use list_sql_tables to find tables, then describe_sql_table to see column schemas, then query.

Supports datasource-specific macros: $__timeFilter(column), $__from/$__to, $__interval, ${varname}

Time formats: 'now-1h', '2026-02-02T19:00:00Z', '1738519200000' (Unix ms)

Example: SELECT timestamp, message FROM logs WHERE $__timeFilter(timestamp) LIMIT 100`,
	querySQLHandler,
	mcp.WithTitleAnnotation("Query SQL"),
	mcp.WithIdempotentHintAnnotation(false),
	mcp.WithReadOnlyHintAnnotation(false),
	mcp.WithDestructiveHintAnnotation(true),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ListSQLDatabases = mcpgrafana.MustTool(
	"list_sql_databases",
	"List databases, schemas, or catalogs from a supported SQL datasource. Returns the organizational units available for use with list_sql_tables. For Athena: omit catalog to list catalogs, or pass catalog to list databases in it.",
	listSQLDatabasesHandler,
	mcp.WithTitleAnnotation("List SQL databases"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var ListSQLTables = mcpgrafana.MustTool(
	"list_sql_tables",
	"START HERE for SQL datasources: List tables from a supported SQL datasource (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL). Returns table names, schemas, and metadata. NEXT: Use describe_sql_table to see column schemas.",
	listSQLTablesHandler,
	mcp.WithTitleAnnotation("List SQL tables"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

var DescribeSQLTable = mcpgrafana.MustTool(
	"describe_sql_table",
	"Get column schema for a table in a supported SQL datasource (ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL). NEXT: Use query_sql with discovered column names.",
	describeSQLTableHandler,
	mcp.WithTitleAnnotation("Describe SQL table"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
	mcp.WithDestructiveHintAnnotation(false),
	mcp.WithOpenWorldHintAnnotation(false),
)

func AddSQLTools(s *server.MCPServer, enableQueryTools bool) {
	if enableQueryTools {
		QuerySQL.Register(s)
	}
	ListSQLDatabases.Register(s)
	ListSQLTables.Register(s)
	DescribeSQLTable.Register(s)
}
