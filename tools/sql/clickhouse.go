package sql

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type clickHouseDialect struct{}

func init() {
	RegisterDialect(&clickHouseDialect{})
}

func (d *clickHouseDialect) DatasourceTypes() []string {
	return []string{ClickHouseDatasourceType}
}

func (d *clickHouseDialect) DefaultFormat() interface{} {
	return 1
}

func (d *clickHouseDialect) ValidateIdentifier(name, field string) error {
	return ValidateSQLIdentifier(name, field)
}

func (d *clickHouseDialect) ExtraQueryPayloadFields(_ QuerySQLParams) map[string]interface{} {
	return nil
}

func (d *clickHouseDialect) SubstituteMacros(query string, from, to time.Time) string {
	fromSeconds := from.Unix()
	toSeconds := to.Unix()
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()

	rangeSeconds := toSeconds - fromSeconds
	intervalSeconds := rangeSeconds / 1000
	if intervalSeconds < 1 {
		intervalSeconds = 1
	}

	timeFilterRe := regexp.MustCompile(`\$__timeFilter\(([^)]+)\)`)
	query = timeFilterRe.ReplaceAllStringFunc(query, func(match string) string {
		submatch := timeFilterRe.FindStringSubmatch(match)
		if len(submatch) > 1 {
			column := strings.TrimSpace(submatch[1])
			return fmt.Sprintf("%s >= toDateTime(%d) AND %s <= toDateTime(%d)", column, fromSeconds, column, toSeconds)
		}
		return match
	})

	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))
	query = strings.ReplaceAll(query, "$__interval", fmt.Sprintf("%ds", intervalSeconds))

	return query
}

func (d *clickHouseDialect) EnforceLimit(query string, limit int) string {
	return EnforceSQLLimit(query, limit, DefaultSQLLimit, MaxSQLLimit)
}

func (d *clickHouseDialect) ListDatabases(ctx context.Context, qr QueryRunner, dsType string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error) {
	query := `SELECT name FROM system.databases WHERE name NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema') ORDER BY name`

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	dbs := make([]SQLDatabaseInfo, 0, len(rows))
	for _, row := range rows {
		dbs = append(dbs, SQLDatabaseInfo{
			Database: ToStringFromRow(row["name"]),
		})
	}
	return &ListSQLDatabasesResult{Databases: dbs}, nil
}

func (d *clickHouseDialect) ListTables(ctx context.Context, qr QueryRunner, dsType string, params ListSQLTablesParams) (*ListSQLTablesResult, error) {
	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}

	query := `SELECT database, name, engine, total_rows, total_bytes
FROM system.tables
WHERE database NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema')`
	if params.Database != "" {
		query += fmt.Sprintf(" AND database = '%s'", params.Database)
	}
	query += " ORDER BY database, name LIMIT 500"

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	tables := make([]SQLTableInfo, 0, len(rows))
	for _, row := range rows {
		tables = append(tables, SQLTableInfo{
			Database: ToStringFromRow(row["database"]),
			Name:     ToStringFromRow(row["name"]),
			Engine:   ToStringFromRow(row["engine"]),
			RowCount: ToInt64FromRow(row["total_rows"]),
			Bytes:    ToInt64FromRow(row["total_bytes"]),
		})
	}
	return &ListSQLTablesResult{Tables: tables}, nil
}

func (d *clickHouseDialect) DescribeTable(ctx context.Context, qr QueryRunner, dsType string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error) {
	if params.Table == "" {
		return nil, fmt.Errorf("table is required")
	}

	database := params.Database
	if database == "" {
		database = "default"
	}

	if err := d.ValidateIdentifier(database, "database"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(params.Table, "table"); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`SELECT name, type, default_kind, default_expression, comment
FROM system.columns
WHERE database = '%s' AND table = '%s'
ORDER BY position`, database, params.Table)

	cols, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}
	_ = cols

	result := make([]SQLColumnInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, SQLColumnInfo{
			Name:    ToStringFromRow(row["name"]),
			Type:    ToStringFromRow(row["type"]),
			Default: ToStringFromRow(row["default_expression"]),
			Comment: ToStringFromRow(row["comment"]),
		})
	}
	return &DescribeSQLTableResult{Columns: result}, nil
}
