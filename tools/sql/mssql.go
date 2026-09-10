package sql

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type mssqlDialect struct{}

func init() {
	RegisterDialect(&mssqlDialect{})
}

func (d *mssqlDialect) DatasourceTypes() []string {
	return []string{MSSQLDatasourceType}
}

func (d *mssqlDialect) DefaultFormat() interface{} {
	return "table"
}

func (d *mssqlDialect) ValidateIdentifier(name, field string) error {
	return ValidateSQLIdentifier(name, field)
}

func (d *mssqlDialect) ExtraQueryPayloadFields(_ QuerySQLParams) map[string]interface{} {
	return nil
}

func (d *mssqlDialect) SubstituteMacros(query string, from, to time.Time) string {
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

func (d *mssqlDialect) EnforceLimit(query string, limit int) string {
	effectiveLimit := limit
	if effectiveLimit <= 0 {
		effectiveLimit = DefaultSQLLimit
	}
	if effectiveLimit > MaxSQLLimit {
		effectiveLimit = MaxSQLLimit
	}

	topRe := regexp.MustCompile(`(?i)\bSELECT(\s+DISTINCT)?\s+TOP\s+\(?(\d+)\)?`)
	if topRe.MatchString(query) {
		return topRe.ReplaceAllStringFunc(query, func(match string) string {
			submatch := topRe.FindStringSubmatch(match)
			existing, _ := strconv.Atoi(submatch[2])
			if existing > MaxSQLLimit {
				distinct := submatch[1]
				if distinct == "" {
					return fmt.Sprintf("SELECT TOP %d", MaxSQLLimit)
				}
				return fmt.Sprintf("SELECT%s TOP %d", distinct, MaxSQLLimit)
			}
			return match
		})
	}

	query = strings.TrimSpace(query)
	query = strings.TrimSuffix(query, ";")

	upper := strings.ToUpper(query)
	if strings.Contains(upper, "OFFSET") && strings.Contains(upper, "FETCH") {
		return query
	}

	selectRe := regexp.MustCompile(`(?i)^SELECT(\s+DISTINCT)?\s+`)
	return selectRe.ReplaceAllStringFunc(query, func(match string) string {
		trimmed := strings.TrimRight(match, " \t")
		return fmt.Sprintf("%s TOP %d ", trimmed, effectiveLimit)
	})
}

func (d *mssqlDialect) ListDatabases(ctx context.Context, qr QueryRunner, dsType string, params ListSQLDatabasesParams) (*ListSQLDatabasesResult, error) {
	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}

	from := "INFORMATION_SCHEMA.SCHEMATA"
	if params.Database != "" {
		from = fmt.Sprintf("%s.INFORMATION_SCHEMA.SCHEMATA", params.Database)
	}

	query := fmt.Sprintf(`SELECT CATALOG_NAME, SCHEMA_NAME
FROM %s
WHERE SCHEMA_NAME NOT IN ('INFORMATION_SCHEMA', 'sys', 'guest', 'db_owner', 'db_accessadmin', 'db_securityadmin', 'db_ddladmin', 'db_backupoperator', 'db_datareader', 'db_datawriter', 'db_denydatareader', 'db_denydatawriter')
ORDER BY SCHEMA_NAME`, from)

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

func (d *mssqlDialect) ListTables(ctx context.Context, qr QueryRunner, dsType string, params ListSQLTablesParams) (*ListSQLTablesResult, error) {
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

	query := fmt.Sprintf("SELECT TOP 500 TABLE_CATALOG, TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE\nFROM %s\nWHERE TABLE_SCHEMA NOT IN ('INFORMATION_SCHEMA', 'sys')", from)
	if params.Schema != "" {
		query += fmt.Sprintf(" AND TABLE_SCHEMA = '%s'", params.Schema)
	}
	query += " ORDER BY TABLE_SCHEMA, TABLE_NAME"

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
		})
	}
	return &ListSQLTablesResult{Tables: tables}, nil
}

func (d *mssqlDialect) DescribeTable(ctx context.Context, qr QueryRunner, dsType string, params DescribeSQLTableParams) (*DescribeSQLTableResult, error) {
	if params.Table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if err := d.ValidateIdentifier(params.Database, "database"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(params.Schema, "schema"); err != nil {
		return nil, err
	}
	if err := d.ValidateIdentifier(params.Table, "table"); err != nil {
		return nil, err
	}

	schema := params.Schema
	if schema == "" {
		schema = "dbo"
	}

	from := "INFORMATION_SCHEMA.COLUMNS"
	if params.Database != "" {
		from = fmt.Sprintf("%s.INFORMATION_SCHEMA.COLUMNS", params.Database)
	}

	query := fmt.Sprintf(`SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT
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
		})
	}
	return &DescribeSQLTableResult{Columns: result}, nil
}
