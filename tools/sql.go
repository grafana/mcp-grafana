package tools

import (
	"context"
	"fmt"
	"strings"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/tools/sql"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var (
	DefaultSQLQueryLimit = uint(100)
	MaxSQLQueryLimit     = uint(1000)

	DefaultSQLTablesLimit = uint(10)
	MaxSQLTablesLimit     = uint(100)
)

// SQLDatasourceTypes lists the supported SQL datasource identifiers in Grafana.
var SQLDatasourceTypes = []string{
	sql.MySQLType,
	sql.MSSQLType,
	sql.PostgresType,
}

type ListSQLDatabaseArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
}

type ListSQLDatabaseResult struct {
	Databases []string `json:"databases"`
}

// withSQLQueryLimit applies default and maximum constraints to a query row limit.
func withSQLQueryLimit(limit uint) uint {
	if limit == 0 {
		limit = DefaultSQLQueryLimit
	}
	if limit > MaxSQLQueryLimit {
		limit = MaxSQLQueryLimit
	}

	return limit
}

// withSQLTablesLimit applies default and maximum constraints to the table list limit.
func withSQLTablesLimit(limit uint) uint {
	if limit == 0 {
		limit = DefaultSQLTablesLimit
	}
	if limit > MaxSQLTablesLimit {
		limit = MaxSQLTablesLimit
	}

	return limit
}

// withSQLDefaultLimits returns the default row limit.
func withSQLDefaultLimits() uint {
	// 0 applies default limits
	return withSQLQueryLimit(0)
}

// sqlDataSource identifies and initializes the appropriate SQL engine for a datasource UID.
func sqlDataSource(ctx context.Context, uid string) (sql.SQLDatabase, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	switch ds.Type {
	case sql.MySQLType:
		return sql.NewMySQL(), nil
	case sql.MSSQLType:
		return sql.NewMSSQL(), nil
	case sql.PostgresType:
		return sql.NewPostgres(), nil
	default:
		return nil, fmt.Errorf("datasource %s of type %s,is not an SQL Datasource", uid, ds.Type)
	}
}

func listSQLDatabases(ctx context.Context, args ListSQLDatabaseArgs) (*ListSQLDatabaseResult, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}

	refID := "databases"

	query, _ := datasource.QueryWithLimit(datasource.GetDatabaseQuery(), withSQLDefaultLimits())

	batchResult, err := engine.QueryBatch(ctx, []sql.SQLQuery{
		engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         refID,
			DatasourceUId: args.DatasourceUID,
			Query:         query,
			DB:            &datasource,
		}),
	}, sql.QueryBatchArgs{})

	if err != nil {
		return nil, err
	}

	result := batchResult.Results[refID]

	var databases []string

	if len(result.Frames) > 0 {
		frame := result.Frames[0]
		databases = make([]string, 0, len(frame.Rows))
		for _, row := range frame.Rows {
			if database, ok := row[sql.DatabaseNameColumn].(string); ok && database != "" {
				databases = append(databases, database)
			}
		}
	}

	return &ListSQLDatabaseResult{
		Databases: databases,
	}, nil
}

type ListSQLTablesArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables (lists default database if not specified)"`
}

type ListSQLTableResult struct {
	Tables []string `json:"tables"`
}

func listSQLTables(ctx context.Context, args ListSQLTablesArgs) (*ListSQLTableResult, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}

	refID := "tables"

	query, _ := datasource.QueryWithLimit(datasource.GetTablesQuery(args.Database), withSQLDefaultLimits())

	batchResult, err := engine.QueryBatch(ctx, []sql.SQLQuery{
		engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         refID,
			DatasourceUId: args.DatasourceUID,
			Query:         query,
			DB:            &datasource,
		}),
	}, sql.QueryBatchArgs{})

	if err != nil {
		return nil, err
	}

	result := batchResult.Results[refID]

	if result.Error != "" {
		return nil, fmt.Errorf("downstream error : %s", result.Error)
	}

	// query returns only one frame
	var tables []string

	if len(result.Frames) > 0 {
		frame := result.Frames[0]
		tables = make([]string, 0, len(frame.Rows))
		for _, row := range frame.Rows {
			if table, ok := row[sql.TableNameColumn].(string); ok && table != "" {
				tables = append(tables, table)
			}
		}
	}

	return &ListSQLTableResult{
		Tables: tables,
	}, nil
}

type GetSQLTableSchemaArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables (lists default database if not specified)"`
	Tables        string `json:"tables" jsonschema:"required,description=comma separated list of Table names to retrieve schema"`
}

type SQLSchemaField struct {
	Type string `json:"type"`
}

type SQLSchemaResult struct {
	Name   string                     `json:"name"`
	Fields map[string]*SQLSchemaField `json:"fields"`
}

type ListSQLTableSchemaResult struct {
	Schemas map[string]*SQLSchemaResult `json:"schemas"`
	Errors  []string                    `json:"errors,omitempty"`
}

func listSQLTableSchema(ctx context.Context, args GetSQLTableSchemaArgs) (*ListSQLTableSchemaResult, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}

	tables := strings.Split(args.Tables, ",")
	for i := range tables {
		tables[i] = strings.TrimSpace(tables[i])
	}

	// limit is applied at no
	tablesLen := withSQLTablesLimit(uint(len(tables)))

	queries := make([]sql.SQLQuery, 0, tablesLen)

	for i, table := range tables {
		if i >= int(tablesLen) {
			continue
		}
		queries = append(queries, engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         table,
			DatasourceUId: args.DatasourceUID,
			Query:         datasource.GetSchemaQuery(table, args.Database),
			DB:            &datasource,
		}))
	}

	batchResult, err := engine.QueryBatch(ctx, queries, sql.QueryBatchArgs{})

	if err != nil {
		return nil, err
	}

	result := ListSQLTableSchemaResult{}
	result.Errors = make([]string, 0, batchResult.ErrorCount)
	result.Schemas = make(map[string]*SQLSchemaResult)

	for refID, schemaResult := range batchResult.Results {
		if schemaResult.Error != "" {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", refID, schemaResult.Error))
			continue
		}
		if len(schemaResult.Frames) > 0 {
			frame := schemaResult.Frames[0]
			schema := SQLSchemaResult{
				Name:   refID,
				Fields: map[string]*SQLSchemaField{},
			}
			for _, row := range frame.Rows {
				var colName, colType string
				var ok bool
				colName, ok = row[sql.ColNameColumn].(string)
				if !ok {
					continue
				}
				colType, _ = row[sql.ColTypeColumn].(string)

				if colName != "" {
					schema.Fields[colName] = &SQLSchemaField{
						Type: colType,
					}
				}
			}
			result.Schemas[schema.Name] = &schema
		}
	}

	return &result, nil
}

type SQLQueryArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables (lists default database if not specified)"`
	Query         string `json:"query" jsonschema:"required,description=Raw SQL query. Supports Grafana macros for time-series queries including $__time(column)\\, $__timeEpoch(column)\\, $__timeFilter(column)\\, $__timeFrom()\\, $__timeTo()\\, $__timeGroup(column\\,'interval'\\,[fill])\\, $__timeGroupAlias(column\\,'interval')\\, $__unixEpochFilter(column)\\, $__unixEpochFrom()\\, $__unixEpochTo()\\, $__unixEpochNanoFilter(column)\\, $__unixEpochNanoFrom()\\, $__unixEpochNanoTo()\\, $__unixEpochGroup(column\\,'interval'\\,[fill])\\, $__unixEpochGroupAlias(column\\,'interval'\\,[fill]). Also supports $__from/$__to (time range in ms)\\, $__interval/$__interval_ms (auto interval)\\, and ${varname} for variable substitution. Macro expansion depends on the SQL datasource (MySQL\\, PostgreSQL\\, MSSQL)."`
	Start         string `json:"start,omitempty" jsonschema:"description=Start time for the query. Time formats: 'now-1h'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to 1 hour ago."`
	End           string `json:"end,omitempty" jsonschema:"description=End time for the query. Time formats: 'now'\\, '2026-02-02T19:00:00Z'\\, '1738519200000' (Unix ms). Defaults to now."`
	Limit         uint   `json:"limit,omitempty" jsonschema:"description=Maximum number of rows to return. Default: 100\\, Max: 1000. If query doesn't contain LIMIT\\, one will be appended."`
}

type SQLQueryResponse struct {
	Hints          *EmptyResultHints `json:"hints,omitempty"`
	Result         *sql.SQLQueryResult
	ProcessedQuery string `json:"processedQuery,omitempty"`
}

// sqlQuery handles the query_sql_datasource tool implementation.
func sqlQuery(ctx context.Context, args SQLQueryArgs) (*SQLQueryResponse, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}

	query, ok := datasource.QueryWithLimit(args.Query, withSQLQueryLimit(args.Limit))
	if !ok {
		return nil, fmt.Errorf("query wrapping failed")
	}

	start, end, err := parseTimeRange(args.Start, args.End)

	if err != nil {
		return nil, err
	}

	refID := "rawQuery"

	batchResult, err := engine.QueryBatch(ctx, []sql.SQLQuery{
		engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         refID,
			DatasourceUId: args.DatasourceUID,
			Query:         query,
			DB:            &datasource,
		}),
	}, sql.QueryBatchArgs{
		From: *start,
		To:   *end,
	})

	if err != nil {
		return nil, err
	}

	response := batchResult.Results[refID]

	result := SQLQueryResponse{
		Result: response,
	}

	if len(result.Result.Frames) == 0 {
		result.Hints = GenerateEmptyResultHints(HintContext{
			DatasourceType: datasource.Type(),
			Query:          args.Query,
			ProcessedQuery: query,
		})
	}
	result.ProcessedQuery = query

	return &result, nil
}

var ListSQLDatabases = mcpgrafana.MustTool(
	"list_databases_sql",
	"List databases of an SQL datasource (mysql, mssql, postgres)",
	listSQLDatabases,
	mcp.WithTitleAnnotation("List SQL Databases"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var ListSQLTables = mcpgrafana.MustTool(
	"list_tables_sql",
	"List tables of an SQL datasource (mysql, mssql, postgres)",
	listSQLTables,
	mcp.WithTitleAnnotation("List SQL Tables"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var ListSQLTableSchemas = mcpgrafana.MustTool(
	"list_table_schemas_sql",
	"List schemas of tables in an SQL datasource (mysql, mssql, postgres)",
	listSQLTableSchema,
	mcp.WithTitleAnnotation("List SQL Table Schemas"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

var QuerySQLDatasource = mcpgrafana.MustTool(
	"query_sql_datasource",
	"Execute a raw SQL query on an SQL datasource (mysql, mssql, postgres)",
	sqlQuery,
	mcp.WithTitleAnnotation("Query SQL Datasource"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddSQLTools registers all SQL-related tools with the MCP server.
func AddSQLTools(mcp *server.MCPServer) {
	ListSQLDatabases.Register(mcp)
	ListSQLTables.Register(mcp)
	ListSQLTableSchemas.Register(mcp)
	QuerySQLDatasource.Register(mcp)
}
