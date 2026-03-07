package tools

import (
	"context"
	"fmt"

	mcpgrafana "github.com/grafana/mcp-grafana"
	"github.com/grafana/mcp-grafana/tools/sql"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var (
	SQL_QUERY_ROWS_LIMIT = uint(100)
)
var SQLDatasourceTypes = []string{
	sql.MySQLDatasourceType,
}

type ListSQLDatabaseArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
}
type ListSQLDatabaseResult struct {
}

// validates datasourceUID to be one of supported datasource types
// return non nil error for failed validation
func sqlDataSource(ctx context.Context, uid string) (sql.SQLDataSource, error) {
	ds, err := getDatasourceByUID(ctx, GetDatasourceByUIDParams{UID: uid})
	if err != nil {
		return nil, err
	}

	switch ds.Type {
	case sql.MySQLDatasourceType:
		return sql.NewMySqlDataSource(), nil
	default:
		return nil, fmt.Errorf("datasource %s of type %s,is not an SQL Datasource", uid, ds.Type)
	}
}

func listSQLDatabases(ctx context.Context, args ListSQLDatabaseArgs) (any, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}
	result, err := engine.QueryBatch(ctx, []sql.SQLQuery{
		engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         "databases",
			DatasourceUId: args.DatasourceUID,
			Query:         datasource.GetDatabaseQuery(),
			DB:            &datasource,
		}),
	}, sql.QueryBatchArgs{})

	if err != nil {
		return nil, err
	}

	return result, nil
}

type ListSQLTablesArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables (lists default database if not specified)"`
}

func listSQLTables(ctx context.Context, args ListSQLTablesArgs) (any, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}

	result, err := engine.QueryBatch(ctx, []sql.SQLQuery{
		engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         "tables",
			DatasourceUId: args.DatasourceUID,
			Query:         datasource.GetTablesQuery(args.Database),
			DB:            &datasource,
		}),
	}, sql.QueryBatchArgs{})

	if err != nil {
		return nil, err
	}

	return result, nil
}

type GetSQLTableSchemaArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables (lists default database if not specified)"`
	Table         string `json:"table" jsonschema:"required,description=Table name to retrieve schema"`
}

func getSQLTableSchema(ctx context.Context, args GetSQLTableSchemaArgs) (any, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}

	result, err := engine.QueryBatch(ctx, []sql.SQLQuery{
		engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         "schema",
			DatasourceUId: args.DatasourceUID,
			Query:         datasource.GetSchemaQuery(args.Table, args.Database),
			DB:            &datasource,
		}),
	}, sql.QueryBatchArgs{})

	if err != nil {
		return nil, err
	}

	return result, nil
}

type SQLQueryArgs struct {
	DatasourceUID string `json:"datasourceUid" jsonschema:"required,description=The UID of the SQL datasource"`
	Database      string `json:"database,omitempty" jsonschema:"description=Database name to filter tables (lists default database if not specified)"`
	//TODO: update macros for each datasource
	Query string `json:"query" jsonschema:"required,description=Raw SQL query. Supports  macros: $__timeFilter(column) for time filtering\\, $__from/$__to for millisecond timestamps\\, $__interval/$__interval_ms for calculated intervals\\, and ${varname} for variable substitution."`
}

func sqlQuery(ctx context.Context, args SQLQueryArgs) (any, error) {
	datasource, err := sqlDataSource(ctx, args.DatasourceUID)
	if err != nil {
		return nil, err
	}
	engine, err := sql.NewSQLEngine(ctx)
	if err != nil {
		return nil, err
	}

	query, ok := datasource.QueryWithLimit(args.Query, uint(SQL_QUERY_ROWS_LIMIT))
	if ok {
		return nil, fmt.Errorf("query wrapping failed")
	}

	result, err := engine.QueryBatch(ctx, []sql.SQLQuery{
		engine.BuildQuery(sql.BuildQueryArgs{
			RefID:         "rawQuery",
			DatasourceUId: args.DatasourceUID,
			Query:         query,
			DB:            &datasource,
		}),
	}, sql.QueryBatchArgs{})

	if err != nil {
		return nil, err
	}

	return result, nil
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

var GetSQLTableSchema = mcpgrafana.MustTool(
	"get_table_schema_sql",
	"Get schema of a table in an SQL datasource (mysql, mssql, postgres)",
	getSQLTableSchema,
	mcp.WithTitleAnnotation("Get SQL Table Schema"),
	mcp.WithIdempotentHintAnnotation(true),
	mcp.WithReadOnlyHintAnnotation(true),
)

// AddSQLTools registers all SQL tools with the MCP server
func AddSQLTools(mcp *server.MCPServer) {
	ListSQLDatabases.Register(mcp)
	ListSQLTables.Register(mcp)
	GetSQLTableSchema.Register(mcp)
}
