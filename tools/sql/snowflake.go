package sql

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const snowflakeTimeFormat = "2006-01-02 15:04:05"

type snowflakeDialect struct{}

func init() {
	RegisterDialect(&snowflakeDialect{})
}

func (d *snowflakeDialect) DatasourceTypes() []string {
	return []string{SnowflakeDatasourceType}
}

func (d *snowflakeDialect) DefaultFormat() interface{} {
	return 1
}

func (d *snowflakeDialect) ValidateIdentifier(name, field string) error {
	return ValidateSQLIdentifier(name, field)
}

func (d *snowflakeDialect) ExtraQueryPayloadFields(_ QuerySQLParams) map[string]interface{} {
	return nil
}

func (d *snowflakeDialect) SubstituteMacros(query string, from, to time.Time) string {
	fromMillis := from.UnixMilli()
	toMillis := to.UnixMilli()
	fromStr := from.UTC().Format(snowflakeTimeFormat)
	toStr := to.UTC().Format(snowflakeTimeFormat)

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
			return fmt.Sprintf("%s >= TO_TIMESTAMP_NTZ('%s') AND %s <= TO_TIMESTAMP_NTZ('%s')",
				column, fromStr, column, toStr)
		}
		return match
	})

	query = strings.ReplaceAll(query, "$__timeFrom", fmt.Sprintf("TO_TIMESTAMP_NTZ('%s')", fromStr))
	query = strings.ReplaceAll(query, "$__timeTo", fmt.Sprintf("TO_TIMESTAMP_NTZ('%s')", toStr))
	query = strings.ReplaceAll(query, "$__from", strconv.FormatInt(fromMillis, 10))
	query = strings.ReplaceAll(query, "$__to", strconv.FormatInt(toMillis, 10))
	query = strings.ReplaceAll(query, "$__interval_ms", strconv.FormatInt(intervalSeconds*1000, 10))
	query = strings.ReplaceAll(query, "$__interval", strconv.FormatInt(intervalSeconds, 10))

	return query
}

func (d *snowflakeDialect) EnforceLimit(query string, limit int) string {
	return EnforceSQLLimit(query, limit, DefaultSQLLimit, MaxSQLLimit)
}

func (d *snowflakeDialect) ListDatabases(ctx context.Context, qr QueryRunner, dsType string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error) {
	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}

	from := "INFORMATION_SCHEMA.SCHEMATA"
	if params.Database != "" {
		from = fmt.Sprintf("%s.INFORMATION_SCHEMA.SCHEMATA", params.Database)
	}

	query := fmt.Sprintf(`SELECT CATALOG_NAME, SCHEMA_NAME
FROM %s
WHERE SCHEMA_NAME NOT IN ('INFORMATION_SCHEMA')
ORDER BY CATALOG_NAME, SCHEMA_NAME`, from)

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	dbs := make([]SQLDatabaseInfo, 0, len(rows))
	for _, row := range rows {
		dbs = append(dbs, SQLDatabaseInfo{
			Database: ToStringFromRow(row["CATALOG_NAME"]),
			Schema:   ToStringFromRow(row["SCHEMA_NAME"]),
		})
	}
	return &ListSQLDatabasesResult{Databases: dbs}, nil
}

func (d *snowflakeDialect) ListTables(ctx context.Context, qr QueryRunner, dsType string, params ListSQLTablesParams) (*ListSQLTablesResult, error) {
	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(params.Schema, "schema"); err != nil {
		return nil, err
	}

	from := "INFORMATION_SCHEMA.TABLES"
	if params.Database != "" {
		from = fmt.Sprintf("%s.INFORMATION_SCHEMA.TABLES", params.Database)
	}

	query := fmt.Sprintf(`SELECT TABLE_CATALOG, TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE, ROW_COUNT, BYTES
FROM %s
WHERE TABLE_SCHEMA NOT IN ('INFORMATION_SCHEMA')`, from)
	if params.Schema != "" {
		query += fmt.Sprintf(" AND TABLE_SCHEMA = '%s'", params.Schema)
	}
	query += " ORDER BY TABLE_CATALOG, TABLE_SCHEMA, TABLE_NAME LIMIT 500"

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	tables := make([]SQLTableInfo, 0, len(rows))
	for _, row := range rows {
		tables = append(tables, SQLTableInfo{
			Database: ToStringFromRow(row["TABLE_CATALOG"]),
			Schema:   ToStringFromRow(row["TABLE_SCHEMA"]),
			Name:     ToStringFromRow(row["TABLE_NAME"]),
			Type:     ToStringFromRow(row["TABLE_TYPE"]),
			RowCount: ToInt64FromRow(row["ROW_COUNT"]),
			Bytes:    ToInt64FromRow(row["BYTES"]),
		})
	}
	return &ListSQLTablesResult{Tables: tables}, nil
}

func (d *snowflakeDialect) DescribeTable(ctx context.Context, qr QueryRunner, dsType string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error) {
	if params.Table == "" {
		return nil, fmt.Errorf("table is required")
	}

	schema := params.Schema
	if schema == "" {
		schema = "PUBLIC"
	}

	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(schema, "schema"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(params.Table, "table"); err != nil {
		return nil, err
	}

	from := "INFORMATION_SCHEMA.COLUMNS"
	if params.Database != "" {
		from = fmt.Sprintf("%s.INFORMATION_SCHEMA.COLUMNS", params.Database)
	}

	query := fmt.Sprintf(`SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COMMENT
FROM %s
WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'
ORDER BY ORDINAL_POSITION`, from, schema, params.Table)

	_, rows, err := qr.RunSQL(ctx, params.DatasourceUID, dsType, query, d.DefaultFormat(), time.Now().Add(-time.Hour), time.Now(), nil)
	if err != nil {
		return nil, err
	}

	result := make([]SQLColumnInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, SQLColumnInfo{
			Name:     ToStringFromRow(row["COLUMN_NAME"]),
			Type:     ToStringFromRow(row["DATA_TYPE"]),
			Nullable: ToStringFromRow(row["IS_NULLABLE"]),
			Default:  ToStringFromRow(row["COLUMN_DEFAULT"]),
			Comment:  ToStringFromRow(row["COMMENT"]),
		})
	}
	return &DescribeSQLTableResult{Columns: result}, nil
}
