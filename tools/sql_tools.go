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

	"github.com/grafana/grafana-openapi-client-go/models"
	mcpgrafana "github.com/grafana/mcp-grafana/v2"
	"github.com/grafana/mcp-grafana/v2/tools/sql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// BigQuery uses different datasource settings from the other SQL plugins. Do not
// let its defaults change the behavior of existing dialects.
func fillBigQueryDefaults(ds *models.DataSource, database, location *string) {
	if ds.Type != sql.BigQueryDatasourceType {
		return
	}
	if database != nil && *database == "" {
		*database = jsonDataString(ds, "defaultProject")
	}
	if location != nil && *location == "" {
		*location = jsonDataString(ds, "processingLocation")
	}
}

func formatBigQueryTemplateVariable(values []string, format string) string {
	name, _, _ := strings.Cut(format, ":")
	if name != "sqlstring" {
		return formatTemplateVariable(values, format)
	}
	// GoogleSQL supports double-quoted strings with backslash escapes, not
	// the doubled-apostrophe SQL literals used by the shared formatter.
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return strings.Join(quoted, ",")
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
	fillBigQueryDefaults(ds, nil, &args.Location)

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
	variables := make(templateVariableValues, len(args.Variables))
	for name, value := range args.Variables {
		variables[name] = []string{value}
	}
	processedQuery = substituteQueryTemplateVariableValues(processedQuery, variables, ds.Type, nil)
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

	fillBigQueryDefaults(ds, &args.Database, nil)
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
	fillBigQueryDefaults(ds, &args.Database, nil)
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
	fillBigQueryDefaults(ds, &args.Database, nil)
	return dialect.DescribeTable(ctx, qr, ds.Type, args)
}

var QuerySQL = mcpgrafana.MustTool(
	"query_sql",
	"Query ClickHouse, Snowflake, Athena, MySQL, PostgreSQL, MSSQL, or BigQuery via Grafana. Supports datasource-specific time macros and template variables. BigQuery requires qualified project.dataset.table references; billing uses the datasource project.",
	querySQLHandler,
	mcpgrafana.WithTitleAnnotation("Query SQL"),
	mcpgrafana.WithIdempotentHintAnnotation(false),
	mcpgrafana.WithReadOnlyHintAnnotation(false),
	mcpgrafana.WithDestructiveHintAnnotation(true),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

var ListSQLDatabases = mcpgrafana.MustTool(
	"list_sql_databases",
	"List databases, schemas, or catalogs. Athena: omit catalog to list catalogs; set it to list databases. BigQuery: list datasets in database (project), defaulting to datasource config.",
	listSQLDatabasesHandler,
	mcpgrafana.WithTitleAnnotation("List SQL databases"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

var ListSQLTables = mcpgrafana.MustTool(
	"list_sql_tables",
	"List SQL tables and available metadata. BigQuery returns table/view names only; schema is the required dataset and database is the project.",
	listSQLTablesHandler,
	mcpgrafana.WithTitleAnnotation("List SQL tables"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

var DescribeSQLTable = mcpgrafana.MustTool(
	"describe_sql_table",
	"Describe SQL columns: names, types, and available metadata. BigQuery includes nested field paths and descriptions; schema is the required dataset and database is the project.",
	describeSQLTableHandler,
	mcpgrafana.WithTitleAnnotation("Describe SQL table"),
	mcpgrafana.WithIdempotentHintAnnotation(true),
	mcpgrafana.WithReadOnlyHintAnnotation(true),
	mcpgrafana.WithDestructiveHintAnnotation(false),
	mcpgrafana.WithOpenWorldHintAnnotation(false),
	mcpgrafana.RequiresPermissions(datasourceQuery...),
)

func AddSQLTools(s *mcp.Server, enableQueryTools bool) {
	if enableQueryTools {
		QuerySQL.Register(s)
	}
	ListSQLDatabases.Register(s)
	ListSQLTables.Register(s)
	DescribeSQLTable.Register(s)
}
