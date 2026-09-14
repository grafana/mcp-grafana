package sql

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type postgresDialect struct{}

func init() {
	RegisterDialect(&postgresDialect{})
}

func (d *postgresDialect) DatasourceTypes() []string {
	return []string{PostgresDatasourceType, PostgresLegacyDatasourceType}
}

func (d *postgresDialect) DefaultFormat() interface{} {
	return "table"
}

func (d *postgresDialect) ValidateIdentifier(name, field string) error {
	return ValidateSQLIdentifier(name, field)
}

func (d *postgresDialect) ExtraQueryPayloadFields(_ QuerySQLParams) map[string]interface{} {
	return nil
}

func (d *postgresDialect) SubstituteMacros(query string, from, to time.Time) string {
	fromStr := from.UTC().Format("2006-01-02 15:04:05")
	toStr := to.UTC().Format("2006-01-02 15:04:05")
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()

	rangeSeconds := to.Unix() - from.Unix()
	intervalSeconds := rangeSeconds / 1000
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}

	timeFilterRe := regexp.MustCompile(`\$__timeFilter\(([^)]+)\)`)
	query = timeFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := timeFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			column := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s BETWEEN '%s' AND '%s'", column, fromStr, toStr)
		}
		return match
	})

	query = strings.ReplaceAll(query, "$__timeFrom()", fmt.Sprintf("'%s'::timestamp", fromStr))
	query = strings.ReplaceAll(query, "$__timeTo()", fmt.Sprintf("'%s'::timestamp", toStr))
	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))
	query = strings.ReplaceAll(query, "$__interval", fmt.Sprintf("%d", intervalSeconds))

	return query
}

func (d *postgresDialect) EnforceLimit(query string, limit int) string {
	return EnforceSQLLimit(query, limit, DefaultSQLLimit, MaxSQLLimit)
}

func (d *postgresDialect) ListDatabases(ctx context.Context, qr QueryRunner, dsType string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error) {
	query := `SELECT schema_name
FROM information_schema.schemata
WHERE schema_name NOT IN ('information_schema', 'pg_catalog')
ORDER BY schema_name`

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	dbs := make([]SQLDatabaseInfo, 0, len(rows))
	for _, row := range rows {
		dbs = append(dbs, SQLDatabaseInfo{
			Schema: ToStringFromRow(row["schema_name"]),
		})
	}
	return &ListSQLDatabasesResult{Databases: dbs}, nil
}

func (d *postgresDialect) ListTables(ctx context.Context, qr QueryRunner, dsType string, params ListSQLTablesParams) (*ListSQLTablesResult, error) {
	if err := d.ValidateIdentifier(params.Schema, "schema"); err != nil {
		return nil, err
	}

	query := `SELECT table_schema, table_name, table_type
FROM information_schema.tables
WHERE table_schema NOT IN ('information_schema', 'pg_catalog')`
	if params.Schema != "" {
		query += fmt.Sprintf(" AND table_schema = '%s'", params.Schema)
	}
	query += " ORDER BY table_schema, table_name LIMIT 500"

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	tables := make([]SQLTableInfo, 0, len(rows))
	for _, row := range rows {
		tables = append(tables, SQLTableInfo{
			Schema: ToStringFromRow(row["table_schema"]),
			Name:   ToStringFromRow(row["table_name"]),
			Type:   ToStringFromRow(row["table_type"]),
		})
	}
	return &ListSQLTablesResult{Tables: tables}, nil
}

func (d *postgresDialect) DescribeTable(ctx context.Context, qr QueryRunner, dsType string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error) {
	if params.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if err := d.ValidateIdentifier(params.Schema, "schema"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(params.Table, "table"); err != nil {
		return nil, err
	}

	schema := params.Schema
	if schema == "" {
		schema = "public"
	}

	query := fmt.Sprintf(`SELECT column_name, data_type, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = '%s' AND table_name = '%s'
ORDER BY ordinal_position`, schema, params.Table)

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	result := make([]SQLColumnInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, SQLColumnInfo{
			Name:     ToStringFromRow(row["column_name"]),
			Type:     ToStringFromRow(row["data_type"]),
			Nullable: ToStringFromRow(row["is_nullable"]),
			Default:  ToStringFromRow(row["column_default"]),
		})
	}
	return &DescribeSQLTableResult{Columns: result}, nil
}
