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

	// Whole-token replacement only: the ClickHouse plugin expands its own macros server-side
	// ($__fromTime, $__toTime, $__fromTime_ms, $__toTime_ms, $__interval_s, $__timeInterval, ...),
	// and a plain substring replace of $__from, $__to or $__interval would corrupt them.
	query = replaceWholeMacros(query, map[string]string{
		"from":        strconv.FormatInt(fromMillis, 10),
		"to":          strconv.FormatInt(toMillis, 10),
		"interval_ms": strconv.FormatInt(intervalSeconds*1000, 10),
		"interval":    fmt.Sprintf("%ds", intervalSeconds),
	})

	return query
}

func (d *clickHouseDialect) EnforceLimit(query string, limit int) string {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if before, ok := clickHouseSettingsStart(trimmed); ok {
		limited := EnforceSQLLimit(trimmed[:before], limit, DefaultSQLLimit, MaxSQLLimit)
		suffix := trimmed[before:]
		if len(suffix) > 0 && !strings.ContainsRune(" \t\r\n", rune(suffix[0])) {
			suffix = " " + suffix
		}
		return limited + suffix
	}
	return EnforceSQLLimit(query, limit, DefaultSQLLimit, MaxSQLLimit)
}

var clickHouseHeredoc = regexp.MustCompile(`^\$[a-zA-Z0-9_]*\$`)

// clickHouseSettingsStart finds the end of the expression before a SETTINGS clause
// belonging to a top-level SELECT. Keep intervening comments in the suffix, so an
// inserted LIMIT cannot become part of a trailing line comment. This only locates
// SETTINGS; the shared limit helper retains its existing handling of LIMIT expressions.
func clickHouseSettingsStart(query string) (int, bool) {
	depth, lastCode := 0, 0
	selectSeen := false
	for i := 0; i < len(query); {
		if next := skipClickHouseTrivia(query, i); next > i {
			i = next
			continue
		}
		switch {
		case query[i] == '$' && clickHouseHeredoc.MatchString(query[i:]):
			delimiter := clickHouseHeredoc.FindString(query[i:])
			i += len(delimiter)
			end := strings.Index(query[i:], delimiter)
			if end < 0 {
				return 0, false
			}
			i += end + len(delimiter)
			lastCode = i
		case query[i] == '\'' || query[i] == '"' || query[i] == '`':
			i = skipClickHouseQuoted(query, i)
			lastCode = i
		case query[i] == '(':
			depth++
			i++
			lastCode = i
		case query[i] == ')':
			depth--
			i++
			lastCode = i
		case isClickHouseWordByte(query[i]):
			start := i
			for i < len(query) && isClickHouseWordByte(query[i]) {
				i++
			}
			if depth == 0 && strings.EqualFold(query[start:i], "SELECT") {
				selectSeen = true
			}
			// INSERT and CREATE TABLE can have their own SETTINGS before SELECT.
			if depth == 0 && selectSeen && strings.EqualFold(query[start:i], "SETTINGS") &&
				hasClickHouseSettingAssignment(query, i) {
				return lastCode, true
			}
			lastCode = i
		default:
			i++
			lastCode = i
		}
	}
	return 0, false
}

func hasClickHouseSettingAssignment(query string, i int) bool {
	i = skipClickHouseTrivia(query, i)
	start := i
	if i < len(query) && (query[i] == '"' || query[i] == '`') {
		i = skipClickHouseQuoted(query, i)
	} else {
		for i < len(query) && isClickHouseWordByte(query[i]) {
			i++
		}
	}
	if i == start {
		return false
	}
	i = skipClickHouseTrivia(query, i)
	return i < len(query) && query[i] == '='
}

func skipClickHouseQuoted(query string, i int) int {
	quote := query[i]
	i++
	for i < len(query) {
		if query[i] == '\\' && i+1 < len(query) {
			i += 2
		} else if query[i] == quote {
			i++
			if i < len(query) && query[i] == quote {
				i++
				continue
			}
			return i
		} else {
			i++
		}
	}
	return i
}

func skipClickHouseTrivia(query string, i int) int {
	for i < len(query) {
		switch {
		case strings.ContainsRune(" \t\r\n", rune(query[i])):
			i++
		case strings.HasPrefix(query[i:], "--") || strings.HasPrefix(query[i:], "//") || query[i] == '#':
			for i < len(query) && query[i] != '\n' {
				i++
			}
		case strings.HasPrefix(query[i:], "/*"):
			i += 2
			comments := 1
			for i < len(query) && comments > 0 {
				switch {
				case strings.HasPrefix(query[i:], "/*"):
					comments++
					i += 2
				case strings.HasPrefix(query[i:], "*/"):
					comments--
					i += 2
				default:
					i++
				}
			}
		default:
			return i
		}
	}
	return i
}

func isClickHouseWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_' || c >= 0x80
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
