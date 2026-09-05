package sql

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type mysqlDialect struct{}

func init() {
	RegisterDialect(&mysqlDialect{})
}

func (d *mysqlDialect) DatasourceTypes() []string {
	return []string{MySQLDatasourceType}
}

func (d *mysqlDialect) DefaultFormat() interface{} {
	return "table"
}

func (d *mysqlDialect) ValidateIdentifier(name, field string) error {
	return ValidateSQLIdentifier(name, field)
}

func (d *mysqlDialect) ExtraQueryPayloadFields(_ QuerySQLParams) map[string]interface{} {
	return nil
}

func (d *mysqlDialect) SubstituteMacros(query string, from, to time.Time) string {
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

	query = strings.ReplaceAll(query, "$__timeFrom()", fmt.Sprintf("'%s'", fromStr))
	query = strings.ReplaceAll(query, "$__timeTo()", fmt.Sprintf("'%s'", toStr))
	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))
	query = strings.ReplaceAll(query, "$__interval", fmt.Sprintf("%d", intervalSeconds))

	return query
}

func (d *mysqlDialect) EnforceLimit(query string, limit int) string {
	upper := strings.ToUpper(strings.TrimSpace(query))
	if strings.HasPrefix(upper, "SHOW") || strings.HasPrefix(upper, "DESCRIBE") || strings.HasPrefix(upper, "SET") {
		return query
	}
	return EnforceSQLLimit(query, limit, DefaultSQLLimit, MaxSQLLimit)
}

func (d *mysqlDialect) ListDatabases(ctx context.Context, qr QueryRunner, dsType string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error) {
	query := `SELECT SCHEMA_NAME
FROM INFORMATION_SCHEMA.SCHEMATA
WHERE SCHEMA_NAME NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
ORDER BY SCHEMA_NAME`

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	dbs := make([]SQLDatabaseInfo, 0, len(rows))
	for _, row := range rows {
		dbs = append(dbs, SQLDatabaseInfo{
			Database: ToStringFromRow(row["SCHEMA_NAME"]),
		})
	}
	return &ListSQLDatabasesResult{Databases: dbs}, nil
}

func (d *mysqlDialect) ListTables(ctx context.Context, qr QueryRunner, dsType string, params ListSQLTablesParams) (*ListSQLTablesResult, error) {
	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}

	query := `SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE
FROM INFORMATION_SCHEMA.TABLES
WHERE TABLE_SCHEMA NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')`
	if params.Database != "" {
		query += fmt.Sprintf(" AND TABLE_SCHEMA = '%s'", params.Database)
	}
	query += " ORDER BY TABLE_SCHEMA, TABLE_NAME LIMIT 500"

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	tables := make([]SQLTableInfo, 0, len(rows))
	for _, row := range rows {
		tables = append(tables, SQLTableInfo{
			Database: ToStringFromRow(row["TABLE_SCHEMA"]),
			Name:     ToStringFromRow(row["TABLE_NAME"]),
			Type:     ToStringFromRow(row["TABLE_TYPE"]),
		})
	}
	return &ListSQLTablesResult{Tables: tables}, nil
}

func (d *mysqlDialect) DescribeTable(ctx context.Context, qr QueryRunner, dsType string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error) {
	if params.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(params.Table, "table"); err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_COMMENT
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_NAME = '%s'`, params.Table)
	if params.Database != "" {
		query += fmt.Sprintf(" AND TABLE_SCHEMA = '%s'", params.Database)
	}
	query += " ORDER BY ORDINAL_POSITION"

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	result := make([]SQLColumnInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, SQLColumnInfo{
			Name:     ToStringFromRow(row["COLUMN_NAME"]),
			Type:     ToStringFromRow(row["COLUMN_TYPE"]),
			Nullable: ToStringFromRow(row["IS_NULLABLE"]),
			Default:  ToStringFromRow(row["COLUMN_DEFAULT"]),
			Comment:  ToStringFromRow(row["COLUMN_COMMENT"]),
		})
	}
	return &DescribeSQLTableResult{Columns: result}, nil
}
